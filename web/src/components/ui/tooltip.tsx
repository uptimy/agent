import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

type Tip = { x: number; y: number; content: React.ReactNode };

/**
 * An instant tooltip for dense rows of small targets (uptime bars). The native
 * `title` tooltip waits about a second before appearing and can't be sped up.
 * The tip is portalled to <body> with fixed positioning so parent
 * `overflow: hidden` can't clip it, and nudged to stay inside the viewport.
 *
 * Usage: spread `bind(content)` onto each target and render `tip` once.
 */
export function useTooltip() {
  const [tip, setTip] = useState<Tip | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const [shift, setShift] = useState(0);

  const hide = useCallback(() => setTip(null), []);

  const bind = useCallback(
    (content: React.ReactNode) => {
      const show = (el: Element) => {
        const r = el.getBoundingClientRect();
        setTip({ x: r.left + r.width / 2, y: r.top, content });
      };
      return {
        onPointerEnter: (e: React.PointerEvent) => show(e.currentTarget),
        onPointerLeave: hide,
        // Touch has no hover: a tap shows the tip.
        onPointerDown: (e: React.PointerEvent) => e.pointerType !== "mouse" && show(e.currentTarget),
      };
    },
    [hide],
  );

  // Keep the tip on screen near the left/right edges.
  useLayoutEffect(() => {
    if (!tip || !ref.current) return;
    const w = ref.current.offsetWidth;
    const margin = 8;
    const left = tip.x - w / 2;
    const right = tip.x + w / 2;
    setShift(
      left < margin ? margin - left : right > window.innerWidth - margin ? window.innerWidth - margin - right : 0,
    );
  }, [tip]);

  // A fixed-position tip would drift away from its bar on scroll; hide it.
  useLayoutEffect(() => {
    if (!tip) return;
    window.addEventListener("scroll", hide, true);
    return () => window.removeEventListener("scroll", hide, true);
  }, [tip, hide]);

  const node = tip
    ? createPortal(
        <div
          ref={ref}
          role="tooltip"
          // The hosted Uptimy tooltip's look (dark glass), so agent and hosted
          // status pages read the same.
          className="pointer-events-none fixed z-50 rounded-[10px] border border-white/[0.09] bg-[rgba(17,19,24,0.94)] px-3 py-2 text-xs whitespace-nowrap text-neutral-50 shadow-[0_12px_32px_-8px_rgba(0,0,0,0.45),0_2px_6px_rgba(0,0,0,0.18),inset_0_1px_0_rgba(255,255,255,0.06)] backdrop-blur-md"
          style={{ left: tip.x + shift, top: tip.y - 10, transform: "translate(-50%, -100%)" }}
        >
          {tip.content}
          <span
            aria-hidden="true"
            className="absolute -bottom-[6px] size-2.5 rotate-45 border-r border-b border-white/[0.09] bg-[rgb(17,19,24)]"
            style={{ left: `calc(50% - ${shift}px - 5px)` }}
          />
        </div>,
        document.body,
      )
    : null;

  return { bind, hide, tip: node };
}
