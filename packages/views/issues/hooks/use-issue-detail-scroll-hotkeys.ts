import { useEffect } from "react";
import {
  isEditableShortcutTarget,
  isPortalLayerShortcutTarget,
} from "@multica/core/shortcuts";
import { isImeComposing } from "@multica/core/utils";

const MAX_ATTEMPTS = 30;
const REQUIRED_STABLE_FRAMES = 2;

export function useIssueDetailScrollHotkeys(scrollContainerEl: HTMLElement | null) {
  useEffect(() => {
    if (!scrollContainerEl) return;

    let cancelBottomScroll: (() => void) | undefined;

    const onKeyDown = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.repeat ||
        isImeComposing(event) ||
        event.metaKey ||
        event.ctrlKey ||
        event.altKey ||
        event.shiftKey ||
        isEditableShortcutTarget(event.target) ||
        isPortalLayerShortcutTarget(event.target) ||
        scrollContainerEl.getClientRects().length === 0
      ) {
        return;
      }

      if (event.key === "Home") {
        event.preventDefault();
        cancelBottomScroll?.();
        cancelBottomScroll = undefined;
        scrollContainerEl.scrollTop = 0;
        return;
      }

      if (event.key !== "End") return;

      event.preventDefault();
      cancelBottomScroll?.();
      cancelBottomScroll = scrollToBottomWithRetry(scrollContainerEl);
    };

    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      cancelBottomScroll?.();
    };
  }, [scrollContainerEl]);
}

function scrollToBottomWithRetry(el: HTMLElement) {
  let cancelled = false;
  let attempts = 0;
  let stableFrames = 0;
  let frameId = 0;

  const target = () => Math.max(0, el.scrollHeight - el.clientHeight);
  el.scrollTop = target();

  const tick = () => {
    if (cancelled || !el.isConnected) return;

    attempts += 1;
    const nextTop = target();
    if (Math.abs(el.scrollTop - nextTop) <= 1) {
      stableFrames += 1;
    } else {
      stableFrames = 0;
      el.scrollTop = nextTop;
    }

    if (stableFrames >= REQUIRED_STABLE_FRAMES || attempts >= MAX_ATTEMPTS) return;
    frameId = requestAnimationFrame(tick);
  };

  frameId = requestAnimationFrame(tick);
  return () => {
    cancelled = true;
    cancelAnimationFrame(frameId);
  };
}
