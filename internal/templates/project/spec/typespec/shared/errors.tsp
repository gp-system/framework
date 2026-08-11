// RFC 9457 Problem Details: mirrors the gpsystem kit's httperr.Problem.
// Every error response in the API uses this shape.
import "@typespec/http";

using Http;

model FieldError {
  field: string;
  rule?: string;
  message: string;
}

@error
model Problem {
  type: string;
  title: string;
  status: int32;
  detail?: string;
  instance?: string;
  errors?: FieldError[];
}

model BadRequest {
  @statusCode statusCode: 400;
  @body body: Problem;
}

model Unauthorized {
  @statusCode statusCode: 401;
  @body body: Problem;
}

model Forbidden {
  @statusCode statusCode: 403;
  @body body: Problem;
}

model NotFound {
  @statusCode statusCode: 404;
  @body body: Problem;
}

model Conflict {
  @statusCode statusCode: 409;
  @body body: Problem;
}

model UnprocessableEntity {
  @statusCode statusCode: 422;
  @body body: Problem;
}

model NoContent {
  @statusCode statusCode: 204;
}

model Created<T> {
  @statusCode statusCode: 201;
  @body body: T;
}
