import React, { useState, useEffect } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  FileText,
  Search,
  Loader2,
  File,
  FileCode,
  Layers,
  BookOpen,
  Sparkles,
  Eye,
  Image,
  Table2,
} from "lucide-react";
import { cn, RAG_API_BASE_URL as API_BASE_URL } from "@/lib/utils";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

interface Corpus {
  corpus_id: string;
  name: string | null;
  corpus_type: string | null;
  document_count: number;
  chunk_count: number;
  vector_count?: number;
  embedding_model?: string;
  embedding_dimension?: number;
  vector_index_type?: string;
}

interface Document {
  doc_id: string;
  title: string | null;
  file_type: string | null;
  page_count: number | null;
  chunk_count: number;
  first_chunk_preview?: string;
  has_images?: boolean;
  has_tables?: boolean;
}

interface DocumentBrowserProps {
  tenantId?: string;
  corpusId?: string;
}

// Default corpora for demo
const DEFAULT_CORPORA: Corpus[] = [
  {
    corpus_id: "velodb_docs",
    name: "VeloDB Docs",
    corpus_type: "documentation",
    document_count: 0,
    chunk_count: 0,
    vector_count: 0,
    embedding_model: "BAAI/bge-m3",
    embedding_dimension: 1024,
    vector_index_type: "ivf",
  },
];

function getFileIcon(fileType: string | null) {
  switch (fileType?.toLowerCase()) {
    case "pdf":
      return <FileText className="h-5 w-5 text-red-500" />;
    case "markdown":
    case "md":
      return <FileCode className="h-5 w-5 text-blue-500" />;
    default:
      return <File className="h-5 w-5 text-gray-500" />;
  }
}

interface ChunkData {
  content: string;
  content_type: string;
  page_number: number | null;
  chunk_index: number;
  asset_url?: string;
  description?: string;
}

interface MultimodalAsset {
  type: "image" | "table";
  content: string;
  asset_url?: string;
  description?: string;
  page_number: number | null;
}

// Preprocess markdown to handle special syntax like :::tip blocks
function preprocessMarkdown(content: string): string {
  let processed = content;

  // Handle ::: admonition blocks (docusaurus/vitepress style)
  // Convert to blockquotes with emoji prefixes
  const icons: Record<string, string> = {
    tip: "💡",
    warning: "⚠️",
    note: "📝",
    info: "ℹ️",
    caution: "⚠️",
    danger: "🚨",
  };

  // Multi-line ::: blocks
  processed = processed.replace(
    /:::(tip|warning|note|info|caution|danger)([^\n]*)\n([\s\S]*?):::/gi,
    (_, type, title, blockContent) => {
      const icon = icons[type.toLowerCase()] || "📝";
      const titleText = title.trim() || type.charAt(0).toUpperCase() + type.slice(1);
      // Convert content to blockquote format (prefix each line with >)
      const quotedContent = blockContent
        .trim()
        .split("\n")
        .map((line: string) => `> ${line}`)
        .join("\n");
      return `\n> ${icon} **${titleText}**\n>\n${quotedContent}\n`;
    }
  );

  // Single-line :::tip content ::: format
  processed = processed.replace(
    /^:::(tip|warning|note|info)\s+(.+?)(?:::)?$/gim,
    (_, type, blockContent) => {
      const icon = icons[type.toLowerCase()] || "📝";
      return `> ${icon} **${type.charAt(0).toUpperCase() + type.slice(1)}:** ${blockContent.trim()}`;
    }
  );

  // Clean up any remaining ::: markers
  processed = processed.replace(/^:::.*$/gim, "");

  return processed;
}

// Full document preview with markdown rendering
function DocumentPreview({ doc, onClose }: { doc: Document; onClose: () => void }) {
  const [fullMarkdown, setFullMarkdown] = useState<string>("");
  const [multimodalAssets, setMultimodalAssets] = useState<MultimodalAsset[]>([]);
  const [assetCount, setAssetCount] = useState<number>(0);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const fetchPreview = async () => {
      try {
        // Primary: Use rendered endpoint (returns markdown with inline base64 images)
        const tenantId = encodeURIComponent("VeloDB Sample");
        const corpusId = encodeURIComponent("velodb_docs");
        const response = await fetch(
          `${API_BASE_URL}/api/v1/documents/${tenantId}/${corpusId}/${doc.doc_id}/rendered`
        );

        if (response.ok) {
          const data = await response.json();
          // Markdown already has base64 inline images - render directly
          setFullMarkdown(preprocessMarkdown(data.content || ""));
          setAssetCount(data.asset_count || 0);
          // No separate multimodal assets needed - images are inline
          setMultimodalAssets([]);
        } else {
          // Fallback: fetch chunks for multimodal data
          const chunksResponse = await fetch(
            `${API_BASE_URL}/api/v1/demo/chunks?doc_id=${doc.doc_id}&limit=200&tenant_id=VeloDB%20Sample&corpus_id=velodb_docs`
          );

          if (chunksResponse.ok) {
            const chunksData = await chunksResponse.json();
            const sortedChunks = (chunksData.chunks || []).sort(
              (a: ChunkData, b: ChunkData) => a.chunk_index - b.chunk_index
            );

            // Extract text content
            const textContent = sortedChunks
              .filter((c: ChunkData) => c.content_type === "text")
              .map((c: ChunkData) => c.content)
              .join("\n\n");
            setFullMarkdown(preprocessMarkdown(textContent));

            // Extract multimodal assets (images, tables) for fallback display
            const assets: MultimodalAsset[] = sortedChunks
              .filter((c: ChunkData) => c.content_type === "image" || c.content_type === "table")
              .map((c: ChunkData) => ({
                type: c.content_type as "image" | "table",
                content: c.content,
                asset_url: c.asset_url,
                description: c.description,
                page_number: c.page_number,
              }));
            setMultimodalAssets(assets);
            setAssetCount(assets.filter(a => a.type === "image").length);
          } else {
            setError(`Failed to load document (${response.status})`);
          }
        }
      } catch (err) {
        console.error("Error fetching document preview:", err);
        setError("Failed to load document preview");
      } finally {
        setIsLoading(false);
      }
    };
    fetchPreview();
  }, [doc.doc_id]);

  return (
    <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
      <DialogHeader className="flex-shrink-0">
        <DialogTitle className="flex items-center gap-2">
          {getFileIcon(doc.file_type)}
          <span className="truncate">{doc.title || doc.doc_id}</span>
          <div className="flex items-center gap-2 ml-auto">
            <Badge variant="outline">{doc.chunk_count} chunks</Badge>
            {assetCount > 0 && (
              <Badge variant="secondary" className="bg-green-100 text-green-700">
                <Image className="h-3 w-3 mr-1" />
                {assetCount} images
              </Badge>
            )}
            {doc.has_tables && (
              <Badge variant="secondary" className="bg-amber-100 text-amber-700">
                <Table2 className="h-3 w-3 mr-1" />
                Tables
              </Badge>
            )}
          </div>
        </DialogTitle>
      </DialogHeader>

      {isLoading ? (
        <div className="flex-1 flex items-center justify-center py-16">
          <Loader2 className="h-8 w-8 animate-spin text-blue-500" />
        </div>
      ) : error ? (
        <div className="flex-1 flex items-center justify-center py-16 text-red-500">
          {error}
        </div>
      ) : (
        <div className="flex-1 mt-4 overflow-y-auto" style={{ maxHeight: "calc(85vh - 100px)" }}>
          <div className="pr-4 pb-8">
            {/* Multimodal Assets Section */}
            {multimodalAssets.length > 0 && (
              <div className="mb-6">
                <h3 className="text-sm font-semibold text-slate-700 mb-3 flex items-center gap-2">
                  <Image className="h-4 w-4 text-green-600" />
                  Document Assets ({multimodalAssets.length})
                </h3>
                <div className="grid grid-cols-2 gap-4">
                  {multimodalAssets.map((asset, idx) => (
                    <div
                      key={idx}
                      className={cn(
                        "rounded-lg border overflow-hidden",
                        asset.type === "image" ? "border-green-200 bg-green-50/30" : "border-amber-200 bg-amber-50/30"
                      )}
                    >
                      {asset.type === "image" ? (
                        <div className="p-2">
                          {asset.asset_url ? (
                            <img
                              src={asset.asset_url.startsWith("/api/") ? `${API_BASE_URL}${asset.asset_url}` : asset.asset_url}
                              alt={asset.description || "Document image"}
                              className="w-full h-40 object-contain rounded bg-white"
                              onError={(e) => {
                                (e.target as HTMLImageElement).style.display = "none";
                              }}
                            />
                          ) : (
                            <div className="w-full h-40 flex items-center justify-center bg-slate-100 rounded">
                              <Image className="h-8 w-8 text-slate-300" />
                            </div>
                          )}
                          {(asset.description || asset.page_number) && (
                            <div className="mt-2 px-1">
                              {asset.description && (
                                <p className="text-xs text-slate-600 line-clamp-2">{asset.description}</p>
                              )}
                              {asset.page_number && (
                                <span className="text-[10px] text-slate-400">Page {asset.page_number}</span>
                              )}
                            </div>
                          )}
                        </div>
                      ) : (
                        <div className="p-3">
                          <div className="flex items-center gap-2 mb-2">
                            <Table2 className="h-4 w-4 text-amber-600" />
                            <span className="text-xs font-medium text-slate-600">Table</span>
                            {asset.page_number && (
                              <span className="text-[10px] text-slate-400 ml-auto">Page {asset.page_number}</span>
                            )}
                          </div>
                          <div className="bg-white rounded border border-slate-200 p-2 max-h-32 overflow-auto">
                            <pre className="text-[10px] text-slate-600 whitespace-pre-wrap font-mono">
                              {asset.content.slice(0, 500)}{asset.content.length > 500 ? "..." : ""}
                            </pre>
                          </div>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Render full markdown content */}
            <div className="prose prose-slate prose-sm max-w-none dark:prose-invert prose-headings:text-slate-800 prose-p:text-slate-600 prose-p:leading-relaxed prose-li:text-slate-600 prose-strong:text-slate-700 prose-blockquote:border-l-blue-400 prose-blockquote:bg-slate-50 prose-blockquote:py-2 prose-blockquote:px-4 prose-blockquote:rounded-r-lg prose-a:text-blue-600 prose-pre:bg-slate-900">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                components={{
                  // Custom blockquote for admonitions
                  blockquote: ({ children }) => (
                    <blockquote className="border-l-4 border-blue-400 bg-blue-50/50 py-3 px-4 my-4 rounded-r-lg not-italic text-slate-700">
                      {children}
                    </blockquote>
                  ),
                  // Custom paragraph spacing
                  p: ({ children }) => (
                    <p className="mb-4 leading-relaxed text-slate-600">{children}</p>
                  ),
                  // Custom list styling
                  ul: ({ children }) => (
                    <ul className="list-disc pl-6 mb-4 space-y-2">{children}</ul>
                  ),
                  ol: ({ children }) => (
                    <ol className="list-decimal pl-6 mb-4 space-y-2">{children}</ol>
                  ),
                  li: ({ children }) => (
                    <li className="text-slate-600">{children}</li>
                  ),
                  // Custom image rendering - handle asset URLs and base64 images
                  img: ({ src, alt }) => {
                    // Determine the actual image URL
                    let imageSrc = src;

                    // Asset URL from API (e.g., /api/v1/assets/...)
                    if (src?.startsWith("/api/")) {
                      imageSrc = `${API_BASE_URL}${src}`;
                    }
                    // Base64 data URL - use as-is
                    else if (src?.startsWith("data:")) {
                      imageSrc = src;
                    }
                    // External URL - use as-is
                    else if (src?.startsWith("http://") || src?.startsWith("https://")) {
                      imageSrc = src;
                    }
                    // Unknown format - show placeholder
                    else {
                      return alt ? (
                        <span className="text-xs text-slate-400 italic">[Image: {alt}]</span>
                      ) : null;
                    }

                    return (
                      <span className="block my-4">
                        <img
                          src={imageSrc}
                          alt={alt || "Document image"}
                          className="max-w-full h-auto rounded-lg border border-slate-200 shadow-sm"
                          onError={(e) => {
                            // On error, replace with placeholder
                            const target = e.target as HTMLImageElement;
                            target.style.display = "none";
                            const placeholder = document.createElement("span");
                            placeholder.className = "text-xs text-slate-400 italic";
                            placeholder.textContent = `[Image: ${alt || "unavailable"}]`;
                            target.parentNode?.appendChild(placeholder);
                          }}
                        />
                        {alt && (
                          <span className="block text-xs text-slate-500 mt-1 italic text-center">
                            {alt}
                          </span>
                        )}
                      </span>
                    );
                  },
                  // Custom table styling
                  table: ({ children }) => (
                    <div className="overflow-x-auto my-4">
                      <table className="min-w-full border-collapse border border-slate-300">
                        {children}
                      </table>
                    </div>
                  ),
                  th: ({ children }) => (
                    <th className="border border-slate-300 bg-slate-100 px-3 py-2 text-left font-semibold">
                      {children}
                    </th>
                  ),
                  td: ({ children }) => (
                    <td className="border border-slate-300 px-3 py-2">{children}</td>
                  ),
                  // Code blocks - improved for SQL and other code
                  code: ({ className, children, ...props }) => {
                    const isInline = !className;
                    if (isInline) {
                      return (
                        <code className="bg-slate-100 text-slate-800 px-1.5 py-0.5 rounded text-[13px] font-mono" {...props}>
                          {children}
                        </code>
                      );
                    }
                    // Block code - better SQL/code display
                    const codeString = String(children).replace(/\n$/, "");
                    const isSql = className?.includes("sql") || /^\s*(SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP|WITH)\b/i.test(codeString);
                    return (
                      <div className="my-4 rounded-lg overflow-hidden border border-slate-700">
                        {isSql && (
                          <div className="bg-slate-800 px-3 py-1.5 text-[10px] text-slate-400 font-medium uppercase tracking-wider border-b border-slate-700">
                            SQL
                          </div>
                        )}
                        <pre className="bg-slate-900 p-4 overflow-x-auto">
                          <code className="text-[13px] leading-relaxed font-mono text-slate-100 whitespace-pre" {...props}>
                            {codeString}
                          </code>
                        </pre>
                      </div>
                    );
                  },
                  // Pre wrapper for code blocks
                  pre: ({ children }) => <>{children}</>,
                  // Headers
                  h1: ({ children }) => (
                    <h1 className="text-xl font-bold mt-6 mb-3 text-slate-800">{children}</h1>
                  ),
                  h2: ({ children }) => (
                    <h2 className="text-lg font-semibold mt-5 mb-2 text-slate-700">{children}</h2>
                  ),
                  h3: ({ children }) => (
                    <h3 className="text-base font-medium mt-4 mb-2 text-slate-600">{children}</h3>
                  ),
                }}
              >
                {fullMarkdown || "No content available"}
              </ReactMarkdown>
            </div>
          </div>
        </div>
      )}
    </DialogContent>
  );
}

export function DocumentBrowser({
  tenantId = "VeloDB Sample",
  corpusId = "velodb_docs",
}: DocumentBrowserProps) {
  const [corpora, setCorpora] = useState<Corpus[]>(DEFAULT_CORPORA);
  const [selectedCorpus, setSelectedCorpus] = useState<string>(corpusId);
  const [documents, setDocuments] = useState<Document[]>([]);
  const [searchQuery, setSearchQuery] = useState("");
  const [isLoadingCorpora, setIsLoadingCorpora] = useState(false);
  const [isLoadingDocs, setIsLoadingDocs] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [previewDoc, setPreviewDoc] = useState<Document | null>(null);

  // Fetch documents and corpus stats
  useEffect(() => {
    const fetchDocuments = async () => {
      setIsLoadingDocs(true);
      setError(null);
      try {
        // Fetch documents
        const response = await fetch(
          `${API_BASE_URL}/api/v1/demo/documents?tenant_id=${encodeURIComponent(tenantId)}&corpus_id=${encodeURIComponent(selectedCorpus)}`
        );
        if (!response.ok) throw new Error("Failed to fetch");
        const data = await response.json();
        setDocuments(data.documents || []);

        // Fetch corpus stats from corpora API (includes vector_count)
        try {
          const corporaResponse = await fetch(
            `${API_BASE_URL}/api/v1/tenants/${encodeURIComponent(tenantId)}/corpora`
          );
          if (corporaResponse.ok) {
            const corporaData = await corporaResponse.json();
            const corpusStats = corporaData.corpora?.find(
              (c: Corpus) => c.corpus_id === selectedCorpus
            );
            if (corpusStats) {
              setCorpora([corpusStats]);
            } else {
              // Fallback if corpus not found in API
              setCorpora([{
                corpus_id: selectedCorpus,
                name: selectedCorpus === "velodb_docs" ? "VeloDB Docs" : selectedCorpus,
                corpus_type: "documentation",
                document_count: data.documents?.length || 0,
                chunk_count: data.documents?.reduce((sum: number, d: Document) => sum + (d.chunk_count || 0), 0) || 0,
              }]);
            }
          }
        } catch {
          // Fallback to document-based stats
          setCorpora([{
            corpus_id: selectedCorpus,
            name: selectedCorpus === "velodb_docs" ? "VeloDB Docs" : selectedCorpus,
            corpus_type: "documentation",
            document_count: data.documents?.length || 0,
            chunk_count: data.documents?.reduce((sum: number, d: Document) => sum + (d.chunk_count || 0), 0) || 0,
          }]);
        }
      } catch (err) {
        console.error(err);
        setError("Could not load documents. Is the API running?");
      } finally {
        setIsLoadingDocs(false);
      }
    };
    fetchDocuments();
  }, [selectedCorpus, tenantId]);

  const filteredDocs = documents.filter(
    (doc) =>
      doc.title?.toLowerCase().includes(searchQuery.toLowerCase()) ||
      doc.doc_id?.toLowerCase().includes(searchQuery.toLowerCase())
  );

  const selectedCorpusData = corpora.find((c) => c.corpus_id === selectedCorpus);
  const totalDocs = corpora.reduce((sum, c) => sum + c.document_count, 0);

  return (
    <>
      <Card className="h-full border-0 shadow-lg bg-gradient-to-b from-white to-slate-50">
        <CardHeader className="pb-3">
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="flex items-center gap-2 text-lg">
                <div className="p-2 rounded-lg bg-blue-100">
                  <BookOpen className="h-4 w-4 text-blue-600" />
                </div>
                Document Library
              </CardTitle>
              <p className="text-xs text-muted-foreground mt-1">
                Click preview to explore content
              </p>
            </div>
            <Badge variant="secondary" className="text-xs">
              {totalDocs} documents
            </Badge>
          </div>
        </CardHeader>

        <CardContent className="space-y-4">
          {error && (
            <div className="bg-red-50 text-red-600 px-3 py-2 rounded text-sm">
              {error}
            </div>
          )}

          {/* Collection tabs */}
          {isLoadingCorpora ? (
            <div className="flex justify-center py-4">
              <Loader2 className="h-5 w-5 animate-spin text-blue-500" />
            </div>
          ) : (
            <div className="flex gap-2 overflow-x-auto pb-1">
              {corpora.map((corpus) => (
                <button
                  key={corpus.corpus_id}
                  onClick={() => setSelectedCorpus(corpus.corpus_id)}
                  className={cn(
                    "flex-shrink-0 px-3 py-2 rounded-lg text-sm transition-all",
                    selectedCorpus === corpus.corpus_id
                      ? "bg-blue-600 text-white shadow-md"
                      : "bg-slate-100 text-slate-600 hover:bg-slate-200"
                  )}
                >
                  <div className="font-medium">{corpus.name || corpus.corpus_id}</div>
                  <div className="text-[10px] opacity-80 mt-0.5">
                    {corpus.document_count} docs
                  </div>
                </button>
              ))}
            </div>
          )}

          {/* Search */}
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
            <Input
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="Search documents..."
              className="pl-9 h-9 text-sm"
            />
          </div>

          {/* Documents list */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-medium text-muted-foreground">
                {selectedCorpusData?.name || "Documents"}
              </span>
              <span className="text-xs text-muted-foreground">
                {filteredDocs.length} items
              </span>
            </div>

            {isLoadingDocs ? (
              <div className="flex justify-center py-8">
                <Loader2 className="h-6 w-6 animate-spin text-blue-500" />
              </div>
            ) : filteredDocs.length === 0 ? (
              <div className="text-center py-8 text-muted-foreground text-sm">
                <FileText className="h-8 w-8 mx-auto mb-2 opacity-30" />
                No documents found
              </div>
            ) : (
              <ScrollArea className="h-[280px]">
                <div className="space-y-2 pr-2">
                  {filteredDocs.map((doc) => (
                    <div
                      key={doc.doc_id}
                      className={cn(
                        "w-full p-3 rounded-lg transition-all",
                        "bg-white border border-slate-200",
                        "hover:border-blue-300 hover:shadow-md group"
                      )}
                    >
                      <div className="flex items-center gap-3">
                        <div className="p-2 rounded bg-slate-50 group-hover:bg-blue-50 transition-colors">
                          {getFileIcon(doc.file_type)}
                        </div>
                        <div className="flex-1 min-w-0">
                          <div className="font-medium text-sm truncate group-hover:text-blue-600">
                            {doc.title || doc.doc_id}
                          </div>
                          <div className="flex items-center gap-3 mt-1 text-xs text-muted-foreground">
                            <span className="flex items-center gap-1">
                              <FileText className="h-3 w-3" />
                              {doc.page_count || 1} pages
                            </span>
                            <span className="flex items-center gap-1">
                              <Layers className="h-3 w-3" />
                              {doc.chunk_count} chunks
                            </span>
                          </div>
                        </div>

                        {/* Preview button */}
                        <button
                          onClick={() => setPreviewDoc(doc)}
                          className="p-2 rounded-lg hover:bg-blue-100 text-slate-400 hover:text-blue-600 transition-colors"
                          title="Preview document"
                        >
                          <Eye className="h-4 w-4" />
                        </button>

                        <Badge variant="outline" className="text-[10px]">
                          {doc.file_type?.toUpperCase() || "DOC"}
                        </Badge>
                      </div>
                    </div>
                  ))}
                </div>
              </ScrollArea>
            )}
          </div>

          {/* Footer */}
          <div className="bg-gradient-to-r from-blue-50 to-cyan-50 rounded-lg p-3 border border-blue-100">
            <div className="flex items-center gap-2">
              <Sparkles className="h-4 w-4 text-blue-500" />
              <p className="text-xs text-slate-600">
                <span className="font-medium">Click preview</span> to explore document content and pages
              </p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Document preview dialog */}
      <Dialog open={!!previewDoc} onOpenChange={(open) => !open && setPreviewDoc(null)}>
        {previewDoc && <DocumentPreview doc={previewDoc} onClose={() => setPreviewDoc(null)} />}
      </Dialog>
    </>
  );
}
