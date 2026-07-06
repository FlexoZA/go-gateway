"use client";

import { useState } from "react";

// usePagination slices an in-memory list into pages. The page is clamped to the
// valid range rather than reset when the list changes, so a live-polling list
// (e.g. logs that grow every few seconds) doesn't yank the viewer back to page 1.
// Feed it the current page's slice to a table and the rest to <Pagination>.
export function usePagination<T>(items: T[], pageSize = 25) {
  const [page, setPage] = useState(1);
  const pageCount = Math.max(1, Math.ceil(items.length / pageSize));
  const curPage = Math.min(page, pageCount);
  const start = (curPage - 1) * pageSize;
  const pageItems = items.slice(start, start + pageSize);
  return { page: curPage, setPage, pageCount, start, total: items.length, pageItems };
}
