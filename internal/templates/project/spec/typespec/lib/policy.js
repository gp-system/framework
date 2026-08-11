// Implementations for the @permission / @policy decorators declared in
// policy.tsp: they record the name as an x-permission / x-policy OpenAPI
// extension on the operation via the @typespec/openapi emitter state.
import { setExtension } from "@typespec/openapi";

export function $permission(context, target, name) {
  setExtension(context.program, target, "x-permission", name);
}

export function $policy(context, target, name) {
  setExtension(context.program, target, "x-policy", name);
}
