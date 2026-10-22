"""Face detection + alignment + embedding with the InsightFace `buffalo_l` ONNX models.

Only onnxruntime / numpy / OpenCV are needed (the `insightface` pip package is
not used, it does not build on recent Pythons):

  models/det_10g.onnx     SCRFD-10G face detector  -> boxes + 5 landmarks
  models/w600k_r50.onnx   ArcFace ResNet-50 (WebFace600K) -> 512-d identity vector

Embeddings are L2-normalized, so inner product == cosine similarity. For this
model, cosine >= ~0.40 is usually the same person; unrelated faces sit near 0.

    fm = FaceModel()
    faces = fm.detect(bgr_image)                  # list of Face, largest-first
    vecs  = fm.embed([fm.align(bgr_image, f) for f in faces])
"""

import os
from dataclasses import dataclass

import cv2
import numpy as np
import onnxruntime as ort

MODEL_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "models")

# Canonical 112x112 landmark positions ArcFace was trained on
# (left eye, right eye, nose tip, left mouth corner, right mouth corner).
ARCFACE_DST = np.array([[38.2946, 51.6963], [73.5318, 51.5014], [56.0252, 71.7366],
                        [41.5493, 92.3655], [70.7299, 92.2041]], dtype=np.float32)


@dataclass
class Face:
    bbox: np.ndarray    # x1, y1, x2, y2 in image pixels
    kps: np.ndarray     # 5 x 2 landmarks
    score: float

    @property
    def area(self):
        return float(max(0, self.bbox[2] - self.bbox[0]) * max(0, self.bbox[3] - self.bbox[1]))


COREML = [("CoreMLExecutionProvider", {"ModelFormat": "MLProgram", "MLComputeUnits": "CPUAndGPU"}),
          "CPUExecutionProvider"]


def _static_model(name, shape):
    """Copy of models/<name>.onnx with a fixed input shape (CoreML cannot run the
    dynamic-shape originals). Cached next to the original."""
    dst = os.path.join(MODEL_DIR, f"{name}_{'x'.join(map(str, shape))}.onnx")
    if not os.path.exists(dst):
        import onnx
        from onnx import shape_inference
        m = onnx.load(os.path.join(MODEL_DIR, f"{name}.onnx"))
        dims = m.graph.input[0].type.tensor_type.shape.dim
        for d, v in zip(dims, shape):
            d.ClearField("dim_param")
            d.dim_value = v
        for o in m.graph.output:
            o.type.tensor_type.ClearField("shape")
        onnx.save(shape_inference.infer_shapes(m), dst + ".tmp")
        os.replace(dst + ".tmp", dst)
    return dst


def _session(name, shape, threads, accel):
    so = ort.SessionOptions()
    so.intra_op_num_threads = threads
    so.inter_op_num_threads = 1
    so.log_severity_level = 3
    if accel:
        return ort.InferenceSession(_static_model(name, shape), sess_options=so, providers=COREML)
    return ort.InferenceSession(os.path.join(MODEL_DIR, f"{name}.onnx"), sess_options=so,
                                providers=["CPUExecutionProvider"])


def _nms(dets, thresh):
    x1, y1, x2, y2, scores = dets.T
    areas = (x2 - x1 + 1) * (y2 - y1 + 1)
    order = scores.argsort()[::-1]
    keep = []
    while order.size:
        i = order[0]
        keep.append(i)
        xx1 = np.maximum(x1[i], x1[order[1:]])
        yy1 = np.maximum(y1[i], y1[order[1:]])
        xx2 = np.minimum(x2[i], x2[order[1:]])
        yy2 = np.minimum(y2[i], y2[order[1:]])
        inter = np.maximum(0.0, xx2 - xx1 + 1) * np.maximum(0.0, yy2 - yy1 + 1)
        ovr = inter / (areas[i] + areas[order[1:]] - inter)
        order = order[np.where(ovr <= thresh)[0] + 1]
    return keep


def _umeyama(src, dst):
    """Least-squares similarity transform (rotation + uniform scale + shift) src -> dst, 2x3."""
    n = src.shape[0]
    mu_s, mu_d = src.mean(0), dst.mean(0)
    s, d = src - mu_s, dst - mu_d
    cov = d.T @ s / n
    u, sig, vt = np.linalg.svd(cov)
    sign = np.eye(2)
    if np.linalg.det(u) * np.linalg.det(vt) < 0:
        sign[1, 1] = -1
    r = u @ sign @ vt
    scale = np.trace(np.diag(sig) @ sign) / (s ** 2).sum() * n
    t = mu_d - scale * r @ mu_s
    return np.hstack([scale * r, t[:, None]]).astype(np.float32)


class FaceModel:
    def __init__(self, det_size=640, threads=4, accel=None, rec_batch=64):
        """accel=True runs on the Apple GPU via CoreML (default on macOS arm64);
        rec_batch is the fixed batch size of the accelerated recognition model."""
        if accel is None:
            accel = "CoreMLExecutionProvider" in ort.get_available_providers()
        self.accel, self.threads = accel, threads
        self.rec_batch = rec_batch if accel else 64
        self.det_size = det_size
        self._det = {}
        self._rec = {}
        self.strides = (8, 16, 32)
        self.num_anchors = 2
        self._centers = {}

    # ---------------------------------------------------------------- detection
    def detect(self, img, thresh=0.5, nms=0.4, det_size=None):
        """Faces in a BGR image, sorted largest-first."""
        size = det_size or self.det_size
        h, w = img.shape[:2]
        scale = size / max(h, w)
        nh, nw = max(1, int(round(h * scale))), max(1, int(round(w * scale)))
        canvas = np.zeros((size, size, 3), dtype=np.uint8)
        canvas[:nh, :nw] = cv2.resize(img, (nw, nh))
        blob = cv2.dnn.blobFromImage(canvas, 1.0 / 128, (size, size), (127.5, 127.5, 127.5), swapRB=True)
        if size not in self._det:
            self._det[size] = _session("det_10g", (1, 3, size, size), self.threads, self.accel)
        det = self._det[size]
        outs = det.run(None, {det.get_inputs()[0].name: blob})

        fmc = len(self.strides)
        all_scores, all_boxes, all_kps = [], [], []
        for i, stride in enumerate(self.strides):
            scores = outs[i].reshape(-1)
            boxes = outs[i + fmc].reshape(-1, 4) * stride
            kps = outs[i + fmc * 2].reshape(-1, 10) * stride
            fh, fw = size // stride, size // stride
            key = (fh, fw, stride)
            if key not in self._centers:
                c = np.stack(np.mgrid[:fh, :fw][::-1], axis=-1).astype(np.float32)
                c = (c * stride).reshape(-1, 2)
                self._centers[key] = np.repeat(c, self.num_anchors, axis=0)
            centers = self._centers[key]
            idx = np.where(scores >= thresh)[0]
            if not idx.size:
                continue
            c, b, k = centers[idx], boxes[idx], kps[idx]
            all_scores.append(scores[idx])
            all_boxes.append(np.stack([c[:, 0] - b[:, 0], c[:, 1] - b[:, 1],
                                       c[:, 0] + b[:, 2], c[:, 1] + b[:, 3]], axis=-1))
            k = k.reshape(-1, 5, 2) + c[:, None, :]
            all_kps.append(k)
        if not all_scores:
            return []
        scores = np.concatenate(all_scores)
        boxes = np.concatenate(all_boxes) / scale
        kps = np.concatenate(all_kps) / scale
        keep = _nms(np.hstack([boxes, scores[:, None]]), nms)
        faces = [Face(boxes[i], kps[i], float(scores[i])) for i in keep]
        faces.sort(key=lambda f: f.area, reverse=True)
        return faces

    @staticmethod
    def main_face(faces, img_shape):
        """The subject of a portrait crop: big and close to the centre (InsightFace's heuristic)."""
        if not faces:
            return None
        cy, cx = img_shape[0] / 2, img_shape[1] / 2

        def rank(f):
            fx, fy = (f.bbox[0] + f.bbox[2]) / 2, (f.bbox[1] + f.bbox[3]) / 2
            return f.area - ((fx - cx) ** 2 + (fy - cy) ** 2) * 2.0
        return max(faces, key=rank)

    # ------------------------------------------------------ alignment + embed
    @staticmethod
    def align(img, face):
        m = _umeyama(face.kps.astype(np.float32), ARCFACE_DST)
        return cv2.warpAffine(img, m, (112, 112), borderValue=0.0)

    def _rec_session(self, batch):
        if batch not in self._rec:
            self._rec[batch] = _session("w600k_r50", (batch, 3, 112, 112), self.threads, self.accel)
        return self._rec[batch]

    def embed(self, aligned):
        """L2-normalized 512-d float32 vectors for a list of 112x112 BGR crops."""
        out = []
        for i in range(0, len(aligned), self.rec_batch):
            chunk = aligned[i:i + self.rec_batch]
            n = len(chunk)
            # the accelerated model has a fixed batch: 1 for single faces, else pad
            batch = 1 if (self.accel and n == 1) else (self.rec_batch if self.accel else n)
            blob = cv2.dnn.blobFromImages(chunk, 1.0 / 127.5, (112, 112),
                                          (127.5, 127.5, 127.5), swapRB=True)
            if blob.shape[0] < batch:
                blob = np.concatenate([blob, np.zeros((batch - n, 3, 112, 112), blob.dtype)])
            rec = self._rec_session(batch)
            out.append(rec.run(None, {rec.get_inputs()[0].name: blob})[0][:n])
        if not out:
            return np.zeros((0, 512), dtype=np.float32)
        v = np.concatenate(out).astype(np.float32)
        return v / np.linalg.norm(v, axis=1, keepdims=True)
