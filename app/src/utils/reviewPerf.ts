export interface ReviewPanelPerfSnapshot {
  active: boolean;
  selectedFilePath: string | null;
  fileCount: number;
  needsReviewFileCount: number;
  autoSkipFileCount: number;
  commentCount: number;
  originalLength: number;
  modifiedLength: number;
}

export interface ReviewPerfSnapshot {
  updatedAt: string | null;
  panel: ReviewPanelPerfSnapshot | null;
}

declare global {
  interface Window {
    __ATTN_REVIEW_PERF_DUMP?: () => ReviewPerfSnapshot;
    __ATTN_REVIEW_PERF_CLEAR?: () => void;
  }
}

let reviewPerfSnapshot: ReviewPerfSnapshot = {
  updatedAt: null,
  panel: null,
};

export function updateReviewPerf(patch: Partial<ReviewPerfSnapshot>) {
  reviewPerfSnapshot = {
    ...reviewPerfSnapshot,
    ...patch,
    updatedAt: new Date().toISOString(),
  };
}

export function clearReviewPerf() {
  reviewPerfSnapshot = {
    updatedAt: new Date().toISOString(),
    panel: null,
  };
}

export function getReviewPerfSnapshot(): ReviewPerfSnapshot {
  return {
    updatedAt: reviewPerfSnapshot.updatedAt,
    panel: reviewPerfSnapshot.panel ? { ...reviewPerfSnapshot.panel } : null,
  };
}

if (typeof window !== 'undefined') {
  window.__ATTN_REVIEW_PERF_DUMP = () => getReviewPerfSnapshot();
  window.__ATTN_REVIEW_PERF_CLEAR = () => clearReviewPerf();
}
