// label(ctx) runs once per row. ctx holds the fields listed in the spec's
// `deps`. It keeps no state, so the spec sets `parallel = true` and every
// generator thread runs its own copy.
function label(ctx) {
    var band = ctx.age >= 65 ? "senior" : ctx.age >= 30 ? "adult" : "young";
    return band + ":" + ctx.id;
}
