// JavaScript generators for examples/all_types.spec.
//
// A function receives the fields generated so far for the current row and
// returns a value, always coerced to a string. Declare in `deps` every field
// it reads, so the field graph orders them first.
//
// Globals persist across rows, which is why a JavaScript field forces
// generation onto a single thread. Prefer `template` unless you need logic.

// Rows seen by this producer, used to show that state survives between rows.
var _rows = 0;

// A VARIANT column: return JSON text and the writer stores it as-is.
// Nested values reach JavaScript as JSON text too, so JSON.parse(ctx.field)
// is how you read an ARRAY, MAP or STRUCT field.
function variant_doc(ctx) {
  _rows += 1;
  var tags = JSON.parse(ctx.c_array);
  return JSON.stringify({
    row: _rows,
    id: ctx.id,
    // LARGEINT and DECIMAL arrive as text, since JS numbers lose precision
    // past 2^53. Keep them as strings rather than converting.
    largeint: ctx.c_largeint,
    tag_count: tags.length,
    busy: tags.length > 1,
  });
}

// A label built from other fields, showing branching a template cannot do.
function tier_label(ctx) {
  var amount = Number(ctx.c_dec_9);
  if (amount > 7000000) return "platinum";
  if (amount > 3000000) return "gold";
  return _rows % 2 === 0 ? "silver" : "bronze";
}
