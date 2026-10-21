import { useEffect, useRef, useState, type ReactNode, type RefObject } from "react";

export function VirtualList({
  count,
  estimateSize,
  overscan = 8,
  scrollRef,
  children,
}: {
  count: number;
  estimateSize: number;
  overscan?: number;
  /** The element that scrolls, when the list sits in its own pane; the window otherwise. */
  scrollRef?: RefObject<HTMLElement | null>;
  children: (index: number) => ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [start, setStart] = useState(0);
  const [end, setEnd] = useState(Math.min(count, 24));

  useEffect(() => {
    const onScroll = () => {
      const el = ref.current;
      if (!el) return;
      const scroller = scrollRef?.current;
      const top = el.getBoundingClientRect().top - (scroller ? scroller.getBoundingClientRect().top : 0);
      const viewport = scroller ? scroller.clientHeight : window.innerHeight;
      const startPx = Math.max(0, -top);
      const nextStart = Math.max(0, Math.floor(startPx / estimateSize) - overscan);
      const visible = Math.ceil(viewport / estimateSize) + overscan * 2;
      setStart(nextStart);
      setEnd(Math.min(count, nextStart + visible));
    };
    onScroll();
    const target: HTMLElement | Window = scrollRef?.current ?? window;
    target.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    return () => {
      target.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
    };
  }, [count, estimateSize, overscan, scrollRef]);

  return (
    <div ref={ref} style={{ height: count * estimateSize, position: "relative" }}>
      <div style={{ position: "absolute", top: start * estimateSize, left: 0, right: 0 }}>
        {Array.from({ length: Math.max(0, end - start) }, (_, i) => children(start + i))}
      </div>
    </div>
  );
}
