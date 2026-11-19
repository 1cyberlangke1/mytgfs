import { useCallback, useMemo, useState } from 'react';

/** 常驻错误最多同时展示的条数，超出时按写入顺序挤掉最早的。 */
const PROBLEM_MAX = 4;

export function useProblems() {
  const [problems, setProblems] = useState<Record<string, string>>({});

  const setProblem = useCallback((key: string, text: string) => {
    setProblems((prev) => {
      if (prev[key] === text) return prev;
      const next = { ...prev, [key]: text };
      const keys = Object.keys(next);
      if (keys.length > PROBLEM_MAX) {
        for (const k of keys.slice(0, keys.length - PROBLEM_MAX)) delete next[k];
      }
      return next;
    });
  }, []);

  const clearProblem = useCallback((key: string) => {
    setProblems((prev) => {
      if (!(key in prev)) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  }, []);

  const problemList = useMemo(() => Object.values(problems), [problems]);

  return { problems, problemList, setProblems, setProblem, clearProblem };
}
