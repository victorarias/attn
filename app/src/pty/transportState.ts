import type { PendingAttachOutputChunk } from './attachPlanning';

export interface PtyTransportState<TAttachContext> {
  hasAttachedRuntime(id: string): boolean;
  listAttachedRuntimeIds(): string[];
  markRuntimeAttached(id: string): void;
  clearRuntime(id: string): void;
  clearRuntimeStream(id: string): void;
  clearStreamCaches(): void;
  pruneDetachedRuntimes(attachableIds: Set<string>): void;
  getLastSeq(id: string): number | undefined;
  setLastSeq(id: string, seq: number): void;
  getQueuedAttachOutputs(id: string): PendingAttachOutputChunk[] | undefined;
  setQueuedAttachOutputs(id: string, chunks: PendingAttachOutputChunk[]): void;
  clearQueuedAttachOutputs(id: string): void;
  getAttachContext(id: string): TAttachContext | undefined;
  setAttachContext(id: string, context?: TAttachContext): void;
}

export function createPtyTransportState<TAttachContext>(): PtyTransportState<TAttachContext> {
  const attachedRuntimeIds = new Set<string>();
  const runtimeSeqById = new Map<string, number>();
  const pendingAttachOutputsById = new Map<string, PendingAttachOutputChunk[]>();
  const attachContextById = new Map<string, TAttachContext>();

  return {
    hasAttachedRuntime(id: string) {
      return attachedRuntimeIds.has(id);
    },

    listAttachedRuntimeIds() {
      return Array.from(attachedRuntimeIds);
    },

    markRuntimeAttached(id: string) {
      attachedRuntimeIds.add(id);
    },

    clearRuntime(id: string) {
      attachedRuntimeIds.delete(id);
      runtimeSeqById.delete(id);
      pendingAttachOutputsById.delete(id);
      attachContextById.delete(id);
    },

    clearRuntimeStream(id: string) {
      runtimeSeqById.delete(id);
      pendingAttachOutputsById.delete(id);
    },

    clearStreamCaches() {
      runtimeSeqById.clear();
      pendingAttachOutputsById.clear();
    },

    pruneDetachedRuntimes(attachableIds: Set<string>) {
      for (const runtimeId of Array.from(attachedRuntimeIds)) {
        if (!attachableIds.has(runtimeId)) {
          attachedRuntimeIds.delete(runtimeId);
          runtimeSeqById.delete(runtimeId);
          pendingAttachOutputsById.delete(runtimeId);
          attachContextById.delete(runtimeId);
        }
      }
    },

    getLastSeq(id: string) {
      return runtimeSeqById.get(id);
    },

    setLastSeq(id: string, seq: number) {
      runtimeSeqById.set(id, seq);
    },

    getQueuedAttachOutputs(id: string) {
      return pendingAttachOutputsById.get(id);
    },

    setQueuedAttachOutputs(id: string, chunks: PendingAttachOutputChunk[]) {
      pendingAttachOutputsById.set(id, chunks);
    },

    clearQueuedAttachOutputs(id: string) {
      pendingAttachOutputsById.delete(id);
    },

    getAttachContext(id: string) {
      return attachContextById.get(id);
    },

    setAttachContext(id: string, context?: TAttachContext) {
      if (context === undefined) {
        attachContextById.delete(id);
        return;
      }
      attachContextById.set(id, context);
    },
  };
}
