"use client";

import { useEffect, useState } from "react";
import { usePagination } from "@/lib/usePagination";

// useTableSearch adds client-side search on top of usePagination. `matches`
// decides whether a row matches the (trimmed, lowercased) query; the page resets
// to 1 whenever the query changes. Returns the current page's slice plus the
// metadata the <Pagination> control needs.
export function useTableSearch<T>(
  items: T[],
  matches: (item: T, q: string) => boolean,
  pageSize = 25,
) {
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const filtered = q ? items.filter((it) => matches(it, q)) : items;
  const pg = usePagination(filtered, pageSize);

  // Reset to the first page whenever the query changes so matches stay in view.
  const { setPage } = pg;
  useEffect(() => {
    setPage(1);
  }, [q, setPage]);

  return { query, setQuery, ...pg };
}
