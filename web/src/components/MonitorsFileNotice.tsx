import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

/**
 * What was wrong with the monitors file when the agent started: held back
 * entirely (a syntax error), or applied without the monitors that have
 * problems. Nothing when the file is fine or there is none.
 */
export function MonitorsFileNotice() {
  const info = useQuery({ queryKey: ["info"], queryFn: api.info });
  const p = info.data?.monitors_file_error;
  if (!p) return null;
  const [first, ...rest] = p.errors;
  return (
    <div role="alert" className="mb-4 rounded-md border border-paused/40 bg-paused/10 px-3 py-2 text-sm">
      <p>
        {p.applied ? (
          <>
            <code className="font-mono text-xs">{p.file}</code> has {p.errors.length === 1 ? "an error" : "errors"}: the
            monitors below weren&apos;t applied and keep their last good settings; the rest were.
          </>
        ) : (
          <>
            <code className="font-mono text-xs">{p.file}</code> has an error and wasn&apos;t applied: {first}
          </>
        )}
      </p>
      {(p.applied ? p.errors : rest).length > 0 && (
        <ul className="mt-1 list-disc pl-5">
          {(p.applied ? p.errors : rest).map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      )}
      <p className="mt-1 text-muted-foreground">Fix the file and restart the agent to apply it.</p>
    </div>
  );
}
