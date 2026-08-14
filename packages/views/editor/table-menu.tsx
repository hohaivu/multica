"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import {
  autoUpdate,
  computePosition,
  flip,
  hide,
  offset,
  shift,
} from "@floating-ui/dom";
import { posToDOMRect, type Editor } from "@tiptap/core";
import {
  BetweenHorizontalEnd,
  BetweenHorizontalStart,
  BetweenVerticalEnd,
  BetweenVerticalStart,
  Columns3,
  Rows3,
  Trash2,
  type LucideIcon,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Separator } from "@multica/ui/components/ui/separator";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { useT } from "../i18n";

function shouldShowTableMenu(editor: Editor): boolean {
  return editor.isEditable && !editor.isDestroyed && editor.isActive("table");
}

function selectionRect(editor: Editor): DOMRect {
  try {
    const { from, to } = editor.state.selection;
    const { node } = editor.view.domAtPos(from);
    const element = node instanceof Element ? node : node.parentElement;
    const cell = element?.closest("td, th");
    if (cell) return cell.getBoundingClientRect();
    return posToDOMRect(editor.view, from, to);
  } catch {
    return new DOMRect();
  }
}

function TableActionButton({
  icon: Icon,
  label,
  onAction,
  onKeyDown,
  tabIndex,
  destructive = false,
}: {
  icon: LucideIcon;
  label: string;
  onAction: () => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLButtonElement>) => void;
  tabIndex: number;
  destructive?: boolean;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={label}
            tabIndex={tabIndex}
            className={
              destructive
                ? "text-destructive hover:text-destructive"
                : undefined
            }
            onClick={(event) => {
              const button = event.currentTarget;
              onAction();
              // Commands deliberately restore the editor selection. Keep
              // keyboard activation in the toolbar so Enter/Space can invoke
              // another action without requiring the user to re-enter it.
              if (event.detail === 0) button.focus();
            }}
            onKeyDown={onKeyDown}
            onMouseDown={(event) => event.preventDefault()}
          />
        }
      >
        <Icon className="size-3.5" />
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={8}>
        {label}
      </TooltipContent>
    </Tooltip>
  );
}

/** Contextual structural controls for the table cell holding the selection. */
function EditorTableMenu({ editor }: { editor: Editor }) {
  const { t } = useT("editor");
  const [visible, setVisible] = useState(() => shouldShowTableMenu(editor));
  const [focusedActionIndex, setFocusedActionIndex] = useState(0);
  const floatingRef = useRef<HTMLDivElement>(null);
  const updatePositionRef = useRef<() => void>(() => {});

  const virtualRef = useMemo(
    () => ({
      getBoundingClientRect: () =>
        editor.isDestroyed ? new DOMRect() : selectionRect(editor),
      contextElement: editor.view.dom,
    }),
    [editor],
  );

  useEffect(() => {
    const onTransaction = () => {
      if (!editor.isInitialized) return;
      const nextVisible = shouldShowTableMenu(editor);
      setVisible(nextVisible);
      if (nextVisible) updatePositionRef.current();
    };
    editor.on("transaction", onTransaction);
    return () => {
      editor.off("transaction", onTransaction);
    };
  }, [editor]);

  useEffect(() => {
    const onBlur = () => {
      setTimeout(() => {
        if (editor.isDestroyed || editor.view.hasFocus()) return;
        if (floatingRef.current?.contains(document.activeElement)) return;
        setVisible(false);
      }, 0);
    };
    editor.on("blur", onBlur);
    return () => {
      editor.off("blur", onBlur);
    };
  }, [editor]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        event.key !== "F10" ||
        event.shiftKey ||
        event.ctrlKey ||
        event.metaKey
      ) {
        return;
      }
      if (!shouldShowTableMenu(editor)) return;
      const firstButton =
        floatingRef.current?.querySelector<HTMLButtonElement>("button");
      if (!firstButton) return;
      event.preventDefault();
      setFocusedActionIndex(0);
      firstButton.focus();
    };

    const editorDom = editor.view?.dom;
    if (!editorDom) return;
    editorDom.addEventListener("keydown", onKeyDown);
    return () => editorDom.removeEventListener("keydown", onKeyDown);
  }, [editor]);

  useEffect(() => {
    const element = floatingRef.current;
    if (!visible || !element || !editor.isInitialized) return;

    const updatePosition = () => {
      void computePosition(virtualRef, element, {
        strategy: "fixed",
        placement: "bottom-start",
        middleware: [
          offset(6),
          flip({ fallbackPlacements: ["top-start"] }),
          shift({ padding: 8 }),
          hide(),
        ],
      }).then(({ x, y, middlewareData }) => {
        if (!element.isConnected) return;
        element.style.visibility = middlewareData.hide?.referenceHidden
          ? "hidden"
          : "visible";
        element.style.left = `${x}px`;
        element.style.top = `${y}px`;
      });
    };

    updatePositionRef.current = updatePosition;
    const cleanup = autoUpdate(virtualRef, element, updatePosition);
    return () => {
      updatePositionRef.current = () => {};
      cleanup();
    };
  }, [visible, editor, virtualRef]);

  useEffect(() => {
    if (!visible) return;
    const onMouseDown = (event: MouseEvent) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (editor.view.dom.contains(target)) return;
      if (floatingRef.current?.contains(target)) return;
      setVisible(false);
    };
    document.addEventListener("mousedown", onMouseDown);
    return () => document.removeEventListener("mousedown", onMouseDown);
  }, [visible, editor]);

  const run = useCallback(
    (
      command: (
        chain: ReturnType<Editor["chain"]>,
      ) => ReturnType<Editor["chain"]>,
    ) => {
      command(editor.chain().focus()).run();
    },
    [editor],
  );

  const handleActionKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLButtonElement>) => {
      if (event.key === "Escape") {
        event.preventDefault();
        editor.commands.focus();
        return;
      }
      if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;

      const buttons = Array.from(
        floatingRef.current?.querySelectorAll<HTMLButtonElement>("button") ??
          [],
      );
      const currentIndex = buttons.indexOf(event.currentTarget);
      if (currentIndex < 0 || buttons.length === 0) return;
      event.preventDefault();
      const direction = event.key === "ArrowRight" ? 1 : -1;
      const nextIndex =
        (currentIndex + direction + buttons.length) % buttons.length;
      setFocusedActionIndex(nextIndex);
      buttons[nextIndex]?.focus();
    },
    [editor],
  );

  if (!visible) return null;

  return (
    <div
      ref={floatingRef}
      role="toolbar"
      aria-label={t(($) => $.table_menu.label)}
      aria-keyshortcuts="F10"
      className="bubble-menu"
      style={{
        position: "fixed",
        zIndex: 50,
        width: "max-content",
        visibility: "hidden",
      }}
      onMouseDown={(event) => {
        event.preventDefault();
        event.stopPropagation();
      }}
    >
      <TooltipProvider delay={300}>
        <TableActionButton
          icon={BetweenHorizontalStart}
          label={t(($) => $.table_menu.add_row_above)}
          onAction={() => run((chain) => chain.addRowBefore())}
          onKeyDown={handleActionKeyDown}
          tabIndex={focusedActionIndex === 0 ? 0 : -1}
        />
        <TableActionButton
          icon={BetweenHorizontalEnd}
          label={t(($) => $.table_menu.add_row_below)}
          onAction={() => run((chain) => chain.addRowAfter())}
          onKeyDown={handleActionKeyDown}
          tabIndex={focusedActionIndex === 1 ? 0 : -1}
        />
        <TableActionButton
          icon={Rows3}
          label={t(($) => $.table_menu.delete_row)}
          onAction={() => run((chain) => chain.deleteRow())}
          onKeyDown={handleActionKeyDown}
          tabIndex={focusedActionIndex === 2 ? 0 : -1}
        />
        <Separator orientation="vertical" className="mx-0.5 h-5" />
        <TableActionButton
          icon={BetweenVerticalStart}
          label={t(($) => $.table_menu.add_column_left)}
          onAction={() => run((chain) => chain.addColumnBefore())}
          onKeyDown={handleActionKeyDown}
          tabIndex={focusedActionIndex === 3 ? 0 : -1}
        />
        <TableActionButton
          icon={BetweenVerticalEnd}
          label={t(($) => $.table_menu.add_column_right)}
          onAction={() => run((chain) => chain.addColumnAfter())}
          onKeyDown={handleActionKeyDown}
          tabIndex={focusedActionIndex === 4 ? 0 : -1}
        />
        <TableActionButton
          icon={Columns3}
          label={t(($) => $.table_menu.delete_column)}
          onAction={() => run((chain) => chain.deleteColumn())}
          onKeyDown={handleActionKeyDown}
          tabIndex={focusedActionIndex === 5 ? 0 : -1}
        />
        <Separator orientation="vertical" className="mx-0.5 h-5" />
        <TableActionButton
          icon={Trash2}
          label={t(($) => $.table_menu.delete_table)}
          onAction={() => run((chain) => chain.deleteTable())}
          onKeyDown={handleActionKeyDown}
          tabIndex={focusedActionIndex === 6 ? 0 : -1}
          destructive
        />
      </TooltipProvider>
    </div>
  );
}

export { EditorTableMenu };
