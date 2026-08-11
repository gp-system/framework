// gpsystem authorization decorators. @permission / @policy attach RBAC
// permission and policy names to operations; they surface as x-permission /
// x-policy extensions in the emitted OpenAPI and drive the generated
// enforcement middleware (see the kit's policy package).
import "./policy.js";

extern dec permission(target: TypeSpec.Reflection.Operation, name: valueof string);
extern dec policy(target: TypeSpec.Reflection.Operation, name: valueof string);
