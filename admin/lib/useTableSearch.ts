"use client";

import { useEffect, useState } from "react";

// useTableSearch adds client-side search + pagination over an in-memory list.
// `matches` decides whether a row matches the (trimmed, lowercased) query; the
// page resets to 1 whenever the query changes. Returns the current page's slice
// plus the metadata the <Pagination> control needs.
export function useTableSearch<T>(
  items: T[],
  matches: (item: T, q: string) => boolean,
  pageSize = 25,
) {
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);

  const q = query.trim().toLowerCase();
  const filtered = q ? items.filter((it) => matches(it, q)) : items;

  // Reset to the first page whenever the query changes so matches stay in view.
  useEffect(() => {
    setPage(1);
  }, [q]);

  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const curPage = Math.min(page, pageCount);
  const start = (curPage - 1) * pageSize;
  const pageItems = filtered.slice(start, start + pageSize);

  return {
    query,
    setQuery,
    page: curPage,
    setPage,
    pageCount,
    start,
    total: filtered.length,
    pageItems,
  };
}
