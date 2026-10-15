import { useInfiniteQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { request, type Stack } from "./api";
export function useStacks() {
  const query = useInfiniteQuery({
    queryKey: ["stacks"],
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      request<Stack[]>(
        "/stacks" +
          (pageParam ? "?cursor=" + encodeURIComponent(pageParam) : ""),
        { signal },
      ),
    getNextPageParam: (page) => page.nextCursor,
    staleTime: 60_000,
    retry: false,
  });
  const { hasNextPage, isFetchingNextPage, isError, fetchNextPage } = query;
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage && !isError)
      void fetchNextPage({ cancelRefetch: false });
  }, [hasNextPage, isFetchingNextPage, isError, fetchNextPage]);
  return { ...query, stacks: query.data?.pages.flatMap((p) => p.data) ?? [] };
}
