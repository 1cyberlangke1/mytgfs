import type { TransferTask } from '../types';

export function mergeTransfers(
  uploads: readonly TransferTask[],
  downloads: readonly TransferTask[],
  queue: readonly TransferTask[]
): TransferTask[] {
  const byJob = new Map<number, TransferTask>();
  for (const q of queue) {
    if (typeof q.queueJobId === 'number') byJob.set(q.queueJobId, q);
  }

  const merged: TransferTask[] = [];
  for (const task of uploads) {
    const jobId = task.queueJobId;
    if (typeof jobId !== 'number') {
      merged.push(task);
      continue;
    }
    const fromQueue = byJob.get(jobId);
    if (!fromQueue) {
      merged.push(task);
      continue;
    }
    // 队列行取代本地上传行，并从队列池里摘掉，避免同一任务出现两次
    byJob.delete(jobId);
    merged.push(fromQueue);
  }

  return [...merged, ...downloads, ...byJob.values()];
}
