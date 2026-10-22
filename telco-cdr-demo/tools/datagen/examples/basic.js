// JavaScript for examples/basic.yaml. The spec's `file` is looked up from
// the working directory first, then beside the spec.

// Globals persist across rows, which is why a JavaScript field puts
// generation on one thread unless the spec sets `parallel: true`.
var _global_seq = 0;

function function_a(ctx) {
    _global_seq += 1;
    // Integers past 2^53 arrive as text, since JavaScript numbers are
    // doubles; everything smaller arrives as a number.
    return "row-" + ctx.id + "/call-" + _global_seq;
}
