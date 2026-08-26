import { useState } from "react";
import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useIssueDetailScrollHotkeys } from "./use-issue-detail-scroll-hotkeys";

let rafCallbacks: Array<{ id: number; callback: FrameRequestCallback }> = [];
let rafId = 0;

function Harness({ visible = true }: { visible?: boolean }) {
  const [scrollContainerEl, setScrollContainerEl] = useState<HTMLDivElement | null>(null);
  useIssueDetailScrollHotkeys(scrollContainerEl);
  return (
    <div
      ref={setScrollContainerEl}
      data-testid="scroller"
      style={{ height: 100, overflowY: "auto", display: visible ? "block" : "none" }}
    >
      <div style={{ height: 2000 }} />
    </div>
  );
}

function flushNextAnimationFrame() {
  act(() => {
    const callbacks = rafCallbacks;
    rafCallbacks = [];
    callbacks.forEach(({ callback }) => callback(performance.now()));
  });
}

describe("useIssueDetailScrollHotkeys", () => {
  beforeEach(() => {
    rafCallbacks = [];
    rafId = 0;
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      rafId += 1;
      rafCallbacks.push({ id: rafId, callback });
      return rafId;
    });
    vi.stubGlobal("cancelAnimationFrame", (id: number) => {
      rafCallbacks = rafCallbacks.filter((request) => request.id !== id);
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  it("scrolls Home to the top", () => {
    const { getByTestId } = render(<Harness />);
    const scroller = getByTestId("scroller") as HTMLElement;
    Object.defineProperty(scroller, "getClientRects", { value: () => [{ }] });
    scroller.scrollTop = 500;
    fireEvent.keyDown(document, { key: "Home" });
    expect(scroller.scrollTop).toBe(0);
  });

  it("retries End until it reaches the bottom", () => {
    const { getByTestId } = render(<Harness />);
    const scroller = getByTestId("scroller") as HTMLElement;
    Object.defineProperty(scroller, "getClientRects", { value: () => [{ }] });
    let height = 500;
    Object.defineProperty(scroller, "scrollHeight", { configurable: true, get: () => height });
    Object.defineProperty(scroller, "clientHeight", { configurable: true, value: 100 });
    fireEvent.keyDown(document, { key: "End" });
    height = 2100;
    flushNextAnimationFrame();
    flushNextAnimationFrame();
    expect(scroller.scrollTop).toBe(2000);
  });

  it.each(["input", "textarea", "contenteditable"])("ignores %s targets", (target) => {
    const { getByTestId } = render(<Harness />);
    const scroller = getByTestId("scroller") as HTMLElement;
    scroller.scrollTop = 500;
    const element = target === "contenteditable"
      ? Object.assign(document.createElement("div"), { contentEditable: "true" })
      : document.createElement(target);
    document.body.appendChild(element);
    fireEvent.keyDown(element, { key: "Home" });
    expect(scroller.scrollTop).toBe(500);
  });

  it("ignores hidden containers", () => {
    const { getByTestId } = render(<Harness visible={false} />);
    const scroller = getByTestId("scroller") as HTMLElement;
    scroller.scrollTop = 500;
    fireEvent.keyDown(document, { key: "Home" });
    expect(scroller.scrollTop).toBe(500);
  });
});
