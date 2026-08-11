import "@typespec/http";

using Http;

// Paged collection wrapper: mirrors the gpsystem kit's paginate.Page[T].
model Paginated<T> {
  items: T[];
  total: int64;
  page: int32;
  perPage: int32;
}

// Cursor-page collection wrapper: mirrors the gpsystem kit's
// paginate.CursorPage[T]. Prefer this over Paginated<T> for large or
// frequently-mutated collections.
model CursorPage<T> {
  @pageItems items: T[];
  @continuationToken nextCursor?: string;
  hasMore: boolean;
}

// Query parameters for a cursor-paginated list operation: spread into the
// operation's parameter list, e.g. op list(...CursorQuery): CursorPage<Item>;
model CursorQuery {
  @query @continuationToken cursor?: string;
  @query @pageSize limit?: int32;
}
