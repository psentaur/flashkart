import { useEffect, useRef, useState } from "react";
import { api } from "./api";

type Result<T> = { data?: T; error?: unknown; loading: boolean; fetchedAt?: string };

export function useQuery<T>(path: string | null, token?: string, poll = 0, stopWhen?: (data: T) => boolean) {
  const shouldStop = useRef(stopWhen);
  useEffect(() => { shouldStop.current = stopWhen; }, [stopWhen]);
  const key = JSON.stringify([path, token]);
  const [result, setResult] = useState<Result<T> & { key: string }>({ key, loading: Boolean(path) });
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!path) return;
    const controller = new AbortController();
    let timer: number | undefined;
    async function load() {
      let complete = false;
      try {
        const data = await api<T>(path!, { token, signal: controller.signal });
        complete = Boolean(shouldStop.current?.(data));
        if (!controller.signal.aborted) setResult({ key, data, loading: false, fetchedAt: new Date().toISOString() });
      } catch (error) {
        if (!controller.signal.aborted) setResult(previous => ({
          ...(previous.key === key ? previous : { key }), error, loading: false,
        }));
      } finally {
        if (poll && !complete && !controller.signal.aborted) timer = window.setTimeout(load, poll);
      }
    }
    void load();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [key, path, token, poll, revision]);
  return {
    ...(result.key === key ? result : { loading: Boolean(path) } as Result<T>),
    reload: () => { setResult(previous => ({ ...previous, key, error: undefined, loading: true })); setRevision(value => value + 1); },
  };
}
