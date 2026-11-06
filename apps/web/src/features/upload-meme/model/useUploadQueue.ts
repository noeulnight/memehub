import { useRef, useState } from 'react';
import type { AxiosInstance } from 'axios';
import { apiError } from '../../../shared/api/client';
import {
  DuplicateMediaError,
  uploadFile,
  validateMediaFile,
  type FileInput,
  type UploadCheckpoint,
} from '../api/upload';

export type UploadItem = {
  id: string;
  file: File;
  metadata: UploadMetadata;
  status: 'queued' | 'uploading' | 'processing' | 'completed' | 'failed' | 'duplicate' | 'skipped';
  progress: number;
  error?: string;
  duplicate?: { id: string; title: string };
  checkpoint: UploadCheckpoint;
  allowDuplicate?: boolean;
};
export type UploadMetadata = Omit<FileInput, 'file'>;

export function useUploadQueue(api: AxiosInstance, onSettled: () => void) {
  const [items, setItems] = useState<UploadItem[]>([]);
  const itemsRef = useRef(items);
  const [running, setRunning] = useState(false);
  const runningRef = useRef(false);
  function publish(next: UploadItem[]) {
    itemsRef.current = next;
    setItems(next);
  }
  function update(id: string, changes: Partial<UploadItem>) {
    publish(itemsRef.current.map((item) => (item.id === id ? { ...item, ...changes } : item)));
  }
  function add(files: File[]) {
    if (runningRef.current) return;
    if (itemsRef.current.length + files.length > 100)
      throw new Error('Choose up to 100 files per batch.');
    publish([
      ...itemsRef.current,
      ...files.map((file) => {
        let error: string | undefined;
        try {
          validateMediaFile(file);
        } catch (cause) {
          error = apiError(cause);
        }
        return {
          id: crypto.randomUUID(),
          file,
          metadata: { title: '', source_url: '', source_name: '', source_author: '' },
          status: error ? ('failed' as const) : ('queued' as const),
          progress: 0,
          error,
          checkpoint: {},
        };
      }),
    ]);
  }
  async function run(ids?: string[], allowDuplicate = false) {
    if (runningRef.current) return;
    const targets = itemsRef.current.filter((item) =>
      ids ? ids.includes(item.id) : ['queued', 'failed'].includes(item.status),
    );
    if (!targets.length) return;
    runningRef.current = true;
    setRunning(true);
    let cursor = 0;
    try {
      await Promise.all(
        Array.from({ length: Math.min(2, targets.length) }, async () => {
          while (cursor < targets.length) {
            const item = targets[cursor++];
            update(item.id, {
              status: 'uploading',
              allowDuplicate: allowDuplicate || item.allowDuplicate,
              progress: 0,
              error: undefined,
              duplicate: undefined,
            });
            try {
              await uploadFile(
                api,
                { ...item.metadata, file: item.file },
                (progress) =>
                  update(item.id, {
                    progress,
                    status: progress === 100 ? 'processing' : 'uploading',
                  }),
                undefined,
                {
                  checkpoint: item.checkpoint,
                  allowDuplicate: allowDuplicate || item.allowDuplicate,
                },
              );
              update(item.id, { status: 'completed', progress: 100 });
            } catch (error) {
              if (error instanceof DuplicateMediaError)
                update(item.id, {
                  status: 'duplicate',
                  duplicate: error.duplicate,
                  error: error.message,
                });
              else update(item.id, { status: 'failed', error: apiError(error) });
            }
          }
        }),
      );
    } finally {
      runningRef.current = false;
      setRunning(false);
      onSettled();
    }
  }
  return {
    items,
    running,
    add,
    run,
    setMetadata: (id: string, metadata: UploadMetadata) => {
      const item = itemsRef.current.find((item) => item.id === id);
      if (
        !runningRef.current &&
        item &&
        !item.checkpoint.id &&
        !['completed', 'skipped'].includes(item.status)
      )
        update(id, { metadata });
    },
    remove: (id: string) => {
      if (!runningRef.current) publish(itemsRef.current.filter((item) => item.id !== id));
    },
    skip: (id: string) => {
      if (!runningRef.current) update(id, { status: 'skipped' });
    },
    reset: () => {
      if (!runningRef.current) publish([]);
    },
  };
}
