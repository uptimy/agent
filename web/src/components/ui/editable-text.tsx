import { useEffect, useRef, useState } from "react";
import { Pencil } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Text that turns into an input when clicked, like the Uptimy app's
 * Editable: Enter or leaving the field saves, Escape cancels.
 */
export function EditableText({
  value,
  onChange,
  label,
  placeholder,
  fallback,
  required = false,
  maxLength,
  startEditing = false,
  className,
}: {
  value: string;
  onChange: (value: string) => void;
  /** Accessible name, e.g. "Section name". */
  label: string;
  /** Shown, muted, when the value is empty. */
  placeholder?: string;
  /** Shown as normal text when the value is empty, e.g. what an empty label falls back to. */
  fallback?: string;
  /** An empty value is refused and the old one kept. */
  required?: boolean;
  maxLength?: number;
  startEditing?: boolean;
  className?: string;
}) {
  const [editing, setEditing] = useState(startEditing);
  const [draft, setDraft] = useState(value);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) input.current?.select();
  }, [editing]);

  const begin = () => {
    setDraft(value);
    setEditing(true);
  };
  const commit = () => {
    const next = draft.trim();
    if (!(required && next === "") && next !== value) onChange(next);
    setEditing(false);
  };

  if (editing) {
    return (
      <input
        ref={input}
        aria-label={label}
        value={draft}
        maxLength={maxLength}
        placeholder={placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            commit();
          } else if (e.key === "Escape") {
            e.stopPropagation();
            setEditing(false);
          }
        }}
        className={cn(
          "h-8 min-w-0 flex-1 rounded-md border border-primary bg-card px-2 text-sm ring-2 ring-primary/20 outline-none",
          className,
        )}
      />
    );
  }
  return (
    <span className={cn("group/editable flex min-w-0 items-center gap-1", className)}>
      <button
        type="button"
        onClick={begin}
        data-tip="Click to rename"
        className="min-w-0 truncate rounded px-1 py-0.5 text-left hover:bg-nav-hover"
      >
        {value || fallback || <span className="font-normal text-muted-foreground italic">{placeholder}</span>}
      </button>
      <button
        type="button"
        onClick={begin}
        aria-label={`Rename: ${label}`}
        className="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground opacity-60 group-hover/editable:opacity-100 hover:bg-nav-hover hover:text-primary focus-visible:opacity-100"
      >
        <Pencil className="size-3.5" />
      </button>
    </span>
  );
}
