import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

/**
 * The agent's one tooltip, the same as the Cloud app's and the hosted status
 * page's (upti.my-app src/components/ui/tip-layer.tsx). Mount <TipLayer /> once;
 * any element carrying `data-tip` shows it on hover and keyboard focus.
 *
 * Plain text: `data-tip="…"`. Structured content: spread `tip({...})`.
 * Never use the native `title` attribute.
 */

export type TipTone = "up" | "minor" | "degraded" | "down" | "info" | "muted";

export interface TipContent {
  title?: string;
  value?: string;
  tone?: TipTone;
  body?: string;
  rows?: { label: string; value: string; tone?: TipTone }[];
  meta?: string;
  placement?: "top" | "bottom";
}

export const tip = (content: TipContent): Record<string, string> => {
  const attrs: Record<string, string> = { "data-tip": content.body ?? "" };
  if (content.title) attrs["data-tip-title"] = content.title;
  if (content.value) attrs["data-tip-value"] = content.value;
  if (content.tone) attrs["data-tip-tone"] = content.tone;
  if (content.meta) attrs["data-tip-meta"] = content.meta;
  if (content.rows?.length) attrs["data-tip-rows"] = JSON.stringify(content.rows);
  if (content.placement) attrs["data-tip-placement"] = content.placement;
  return attrs;
};

const read = (el: HTMLElement): TipContent | null => {
  const d = el.dataset;
  let rows: TipContent["rows"];
  try {
    rows = d.tipRows ? JSON.parse(d.tipRows) : undefined;
  } catch {
    rows = undefined;
  }
  const content: TipContent = {
    title: d.tipTitle,
    value: d.tipValue,
    tone: d.tipTone as TipTone | undefined,
    body: d.tip || undefined,
    rows,
    meta: d.tipMeta,
    placement: d.tipPlacement === "bottom" ? "bottom" : "top",
  };
  return content.title || content.value || content.body || rows?.length ? content : null;
};

const TONE: Record<TipTone, string> = {
  up: "text-emerald-400",
  minor: "text-yellow-400",
  degraded: "text-orange-400",
  down: "text-red-400",
  info: "text-sky-400",
  muted: "text-neutral-400",
};

const SHOW_DELAY_MS = 140;
const WARM_MS = 400;
// Crossing a gap between tipped elements (the space between two bars)
// keeps the tooltip up so it slides to the next one instead of reopening.
const GAP_GRACE_MS = 90;
const GAP = 8;
const EDGE = 8;
const TIP_ID = "uptimy-tip";

interface Shown {
  el: HTMLElement;
  content: TipContent;
  key: string;
}

export default function TipLayer() {
  const [shown, setShown] = useState<Shown | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const arrowRef = useRef<HTMLDivElement>(null);
  const current = useRef<HTMLElement | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const graceTimer = useRef<number | undefined>(undefined);
  const lastVisible = useRef(0);

  useLayoutEffect(() => {
    const box = boxRef.current;
    if (!shown || !box) return;
    const r = shown.el.getBoundingClientRect();
    const { width: w, height: h } = box.getBoundingClientRect();
    const roomAbove = r.top - h - GAP >= EDGE;
    const roomBelow = r.bottom + h + GAP <= window.innerHeight - EDGE;
    const below = shown.content.placement === "bottom" ? roomBelow || !roomAbove : !roomAbove && roomBelow;
    const center = r.left + r.width / 2;
    const x = Math.min(Math.max(center - w / 2, EDGE), window.innerWidth - w - EDGE);
    const y = below ? r.bottom + GAP : r.top - h - GAP;
    const arrowX = Math.min(Math.max(center - x, 12), w - 12);
    // Slide only a tooltip that's already on screen; a fresh one is placed
    // where it belongs (it would otherwise slide in from the top-left corner).
    box.style.transition = box.dataset.placed === "1" ? "transform 120ms cubic-bezier(0.2, 0.8, 0.2, 1)" : "none";
    box.style.transform = `translate(${Math.round(x)}px, ${Math.round(y)}px)`;
    box.dataset.placed = "1";
    box.dataset.side = below ? "bottom" : "top";
    box.style.setProperty("--tip-origin", `${arrowX}px ${below ? "0" : "100%"}`);
    if (arrowRef.current) arrowRef.current.style.left = `${arrowX - 5}px`;
    box.style.visibility = "visible";
  }, [shown]);

  useEffect(() => {
    const open = (el: HTMLElement) => {
      const content = read(el);
      if (!content) return;
      current.current = el;
      el.setAttribute("aria-describedby", TIP_ID);
      setShown({ el, content, key: JSON.stringify(content) });
    };
    const show = (el: HTMLElement) => {
      window.clearTimeout(timer.current);
      window.clearTimeout(graceTimer.current);
      current.current?.removeAttribute("aria-describedby");
      current.current = el;
      if (Date.now() - lastVisible.current < WARM_MS) open(el);
      else timer.current = window.setTimeout(() => current.current === el && open(el), SHOW_DELAY_MS);
    };
    const hide = () => {
      window.clearTimeout(timer.current);
      window.clearTimeout(graceTimer.current);
      if (current.current) {
        current.current.removeAttribute("aria-describedby");
        current.current = null;
      }
      setShown((s) => {
        if (s) lastVisible.current = Date.now();
        return null;
      });
    };
    const tipped = (node: EventTarget | null) =>
      node instanceof Element ? (node.closest("[data-tip]") as HTMLElement | null) : null;
    const onOver = (e: PointerEvent) => {
      if (e.pointerType === "touch") return;
      const el = tipped(e.target);
      if (el && el !== current.current) show(el);
      else if (el) window.clearTimeout(graceTimer.current);
      else if (current.current) {
        window.clearTimeout(graceTimer.current);
        graceTimer.current = window.setTimeout(hide, GAP_GRACE_MS);
      }
    };
    const onOut = (e: PointerEvent) => {
      if (!e.relatedTarget) hide();
    };
    // Taps open the tooltip on touch screens (bars have no hover there).
    const onDown = (e: PointerEvent) => {
      const el = e.pointerType === "touch" ? tipped(e.target) : null;
      if (el && el !== current.current) {
        current.current = el;
        open(el);
      } else hide();
    };
    const onFocusIn = (e: FocusEvent) => {
      const el = tipped(e.target);
      if (el && (e.target as HTMLElement).matches(":focus-visible")) show(el);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && hide();
    const watch = window.setInterval(() => {
      const el = current.current;
      if (!el) return;
      if (!el.isConnected) return hide();
      setShown((s) => {
        if (!s || s.el !== el) return s;
        const content = read(el);
        const key = JSON.stringify(content);
        return content && key !== s.key ? { el, content, key } : s;
      });
    }, 250);

    document.addEventListener("pointerover", onOver);
    document.addEventListener("pointerout", onOut);
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("focusin", onFocusIn);
    document.addEventListener("focusout", hide);
    document.addEventListener("keydown", onKey);
    window.addEventListener("scroll", hide, true);
    window.addEventListener("resize", hide);
    return () => {
      document.removeEventListener("pointerover", onOver);
      document.removeEventListener("pointerout", onOut);
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("focusin", onFocusIn);
      document.removeEventListener("focusout", hide);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", hide, true);
      window.removeEventListener("resize", hide);
      window.clearTimeout(timer.current);
      window.clearTimeout(graceTimer.current);
      window.clearInterval(watch);
    };
  }, []);

  if (!shown) return null;
  const { content } = shown;
  const plain = !content.title && !content.value && !content.rows?.length && !content.meta;

  return createPortal(
    <div
      ref={boxRef}
      id={TIP_ID}
      role="tooltip"
      className="pointer-events-none fixed left-0 top-0 z-[10000] max-w-[300px]"
      style={{
        visibility: "hidden",
      }}
    >
      <div
        className={`tip-surface relative rounded-[10px] border border-white/[0.09] bg-[rgba(17,19,24,0.94)] text-xs leading-[1.45] text-neutral-50 backdrop-blur-md ${
          plain ? "px-2.5 py-1.5" : "px-3 py-2.5"
        } tip-enter`}
      >
        {(content.title || content.value) && (
          <div className="flex items-baseline justify-between gap-4">
            {content.title && <div className="whitespace-nowrap font-semibold">{content.title}</div>}
            {content.value && (
              <div
                className={`flex items-center gap-1.5 whitespace-nowrap text-sm font-semibold tabular-nums ${
                  content.tone ? TONE[content.tone] : ""
                }`}
              >
                {content.tone && content.tone !== "muted" && (
                  <span className="tip-dot h-1.5 w-1.5 rounded-full bg-current" />
                )}
                {content.value}
              </div>
            )}
          </div>
        )}
        {content.body && (
          <div
            className={`whitespace-pre-line ${plain ? "" : "text-neutral-300"} ${
              content.title || content.value ? "mt-1" : ""
            }`}
          >
            {content.body}
          </div>
        )}
        {content.rows?.length ? (
          <div className="mt-2 grid grid-cols-[1fr_auto] gap-x-4 gap-y-0.5 border-t border-white/[0.08] pt-2">
            {content.rows.map((row) => (
              <div className="contents" key={row.label}>
                <div className="truncate text-neutral-300">{row.label}</div>
                <div className={`text-right font-medium tabular-nums ${row.tone ? TONE[row.tone] : ""}`}>
                  {row.value}
                </div>
              </div>
            ))}
          </div>
        ) : null}
        {content.meta && (
          <div className={`text-xs text-neutral-400 ${content.rows?.length ? "mt-2" : "mt-1"}`}>{content.meta}</div>
        )}
        <div
          ref={arrowRef}
          aria-hidden="true"
          className="tip-arrow absolute h-2.5 w-2.5 border-b border-r border-white/[0.09] bg-[rgb(17,19,24)]"
        />
      </div>
    </div>,
    document.body,
  );
}
