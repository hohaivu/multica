import { afterEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Editor } from "@tiptap/core";

const computePositionMock = vi.hoisted(() =>
  vi.fn(async () => ({ x: 12, y: 24, middlewareData: {} })),
);

vi.mock("@floating-ui/dom", () => ({
  autoUpdate: (
    _reference: unknown,
    _floating: unknown,
    update: () => void,
  ) => {
    update();
    return vi.fn();
  },
  computePosition: computePositionMock,
  flip: () => ({ name: "flip" }),
  hide: () => ({ name: "hide" }),
  offset: () => ({ name: "offset" }),
  shift: () => ({ name: "shift" }),
}));

const labels = {
  label: "Edit table",
  add_row_above: "Add row above",
  add_row_below: "Add row below",
  delete_row: "Delete row",
  add_column_left: "Add column left",
  add_column_right: "Add column right",
  delete_column: "Delete column",
  delete_table: "Delete table",
};

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (selector: (value: { table_menu: typeof labels }) => string) =>
      selector({ table_menu: labels }),
  }),
}));

import { EditorTableMenu } from "./table-menu";

type EditorEvent = "transaction" | "blur";
const harnessCleanups = new Set<() => void>();

afterEach(() => {
  for (const cleanup of harnessCleanups) cleanup();
  harnessCleanups.clear();
});

function createEditorHarness() {
  let inTable = false;
  const listeners = new Map<EditorEvent, Set<() => void>>();
  const editorDom = document.createElement("div");
  editorDom.tabIndex = -1;
  document.body.append(editorDom);
  const table = document.createElement("table");
  const row = document.createElement("tr");
  const cell = document.createElement("td");
  row.append(cell);
  table.append(row);
  editorDom.append(table);

  const run = vi.fn(() => true);
  const commands = {
    focus: vi.fn(() => {
      editorDom.focus();
      return commands;
    }),
    addRowBefore: vi.fn(),
    addRowAfter: vi.fn(),
    deleteRow: vi.fn(),
    addColumnBefore: vi.fn(),
    addColumnAfter: vi.fn(),
    deleteColumn: vi.fn(),
    deleteTable: vi.fn(),
    run,
  };
  commands.addRowBefore.mockReturnValue(commands);
  commands.addRowAfter.mockReturnValue(commands);
  commands.deleteRow.mockReturnValue(commands);
  commands.addColumnBefore.mockReturnValue(commands);
  commands.addColumnAfter.mockReturnValue(commands);
  commands.deleteColumn.mockReturnValue(commands);
  commands.deleteTable.mockReturnValue(commands);

  const editor = {
    isDestroyed: false,
    isEditable: true,
    isInitialized: true,
    isActive: vi.fn((name: string) => name === "table" && inTable),
    commands,
    chain: vi.fn(() => commands),
    on: vi.fn((event: EditorEvent, listener: () => void) => {
      const eventListeners = listeners.get(event) ?? new Set();
      eventListeners.add(listener);
      listeners.set(event, eventListeners);
    }),
    off: vi.fn((event: EditorEvent, listener: () => void) => {
      listeners.get(event)?.delete(listener);
    }),
    state: {
      selection: { from: 1, to: 1, empty: true },
    },
    view: {
      dom: editorDom,
      domAtPos: vi.fn(() => ({ node: cell, offset: 0 })),
      hasFocus: vi.fn(() => true),
    },
  } as unknown as Editor;

  const cleanup = () => editorDom.remove();
  harnessCleanups.add(cleanup);

  return {
    commands,
    editor,
    emit(event: EditorEvent) {
      act(() => {
        for (const listener of listeners.get(event) ?? []) listener();
      });
    },
    setInTable(next: boolean) {
      inTable = next;
    },
  };
}

const actions = [
  ["Add row above", "addRowBefore"],
  ["Add row below", "addRowAfter"],
  ["Delete row", "deleteRow"],
  ["Add column left", "addColumnBefore"],
  ["Add column right", "addColumnAfter"],
  ["Delete column", "deleteColumn"],
  ["Delete table", "deleteTable"],
] as const;

describe("EditorTableMenu", () => {
  it("appears when the caret enters a table and hides when it leaves", async () => {
    const harness = createEditorHarness();
    render(<EditorTableMenu editor={harness.editor} />);

    expect(screen.queryByRole("toolbar", { name: "Edit table" })).toBeNull();

    harness.setInTable(true);
    harness.emit("transaction");
    expect(
      await screen.findByRole("toolbar", { name: "Edit table" }),
    ).toBeVisible();

    harness.setInTable(false);
    harness.emit("transaction");
    expect(screen.queryByRole("toolbar", { name: "Edit table" })).toBeNull();
  });

  it.each(actions)(
    "runs the %s table command without dropping editor focus",
    async (label, command) => {
      const harness = createEditorHarness();
      harness.setInTable(true);
      render(<EditorTableMenu editor={harness.editor} />);

      await waitFor(() =>
        expect(screen.getByRole("button", { name: label })).toBeVisible(),
      );
      fireEvent.click(screen.getByRole("button", { name: label }));

      expect(harness.commands.focus).toHaveBeenCalledTimes(1);
      expect(harness.commands[command]).toHaveBeenCalledTimes(1);
      expect(harness.commands.run).toHaveBeenCalledTimes(1);
    },
  );

  it.each(actions)(
    "invokes %s through F10, arrow navigation, and native keyboard activation",
    async (label, command) => {
      const harness = createEditorHarness();
      harness.setInTable(true);
      const user = userEvent.setup();
      render(<EditorTableMenu editor={harness.editor} />);

      await waitFor(() =>
        expect(screen.getByRole("button", { name: label })).toBeVisible(),
      );
      const button = screen.getByRole("button", { name: label });
      const actionIndex = actions.findIndex(
        ([actionLabel]) => actionLabel === label,
      );

      harness.editor.view.dom.focus();
      fireEvent.keyDown(harness.editor.view.dom, { key: "F10" });
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: actions[0][0] }),
      );

      for (let index = 0; index < actionIndex; index += 1) {
        await user.keyboard("{ArrowRight}");
      }
      expect(document.activeElement).toBe(button);

      await user.keyboard("{Enter}");
      await user.keyboard(" ");

      expect(harness.commands.focus).toHaveBeenCalledTimes(2);
      expect(harness.commands[command]).toHaveBeenCalledTimes(2);
      expect(harness.commands.run).toHaveBeenCalledTimes(2);
      // The real Tiptap focus command returns focus to the editor. The menu
      // explicitly restores keyboard focus to the activated button afterward.
      expect(document.activeElement).toBe(button);
    },
  );

  it("does not bubble chrome clicks to an editor container", async () => {
    const harness = createEditorHarness();
    const onContainerMouseDown = vi.fn();
    harness.setInTable(true);
    render(
      <div onMouseDown={onContainerMouseDown}>
        <EditorTableMenu editor={harness.editor} />
      </div>,
    );

    await waitFor(() =>
      expect(screen.getByRole("toolbar", { name: "Edit table" })).toBeVisible(),
    );
    const separator = screen.getAllByRole("separator")[0];
    if (!separator) throw new Error("table menu separator not rendered");
    fireEvent.mouseDown(separator);

    expect(onContainerMouseDown).not.toHaveBeenCalled();
    expect(screen.getByRole("toolbar", { name: "Edit table" })).toBeVisible();
  });

  it("returns focus to the editor when Escape leaves the toolbar", async () => {
    const harness = createEditorHarness();
    harness.setInTable(true);
    const user = userEvent.setup();
    render(<EditorTableMenu editor={harness.editor} />);

    await waitFor(() =>
      expect(screen.getByRole("toolbar", { name: "Edit table" })).toBeVisible(),
    );
    harness.editor.view.dom.focus();
    fireEvent.keyDown(harness.editor.view.dom, { key: "F10" });
    await user.keyboard("{Escape}");

    expect(harness.commands.focus).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(harness.editor.view.dom);
  });

  it("keeps a focused menu open on editor blur and hides after focus leaves it", async () => {
    const harness = createEditorHarness();
    harness.setInTable(true);
    render(<EditorTableMenu editor={harness.editor} />);

    await waitFor(() =>
      expect(screen.getByRole("toolbar", { name: "Edit table" })).toBeVisible(),
    );
    const button = screen.getByRole("button", { name: "Add row above" });
    button.focus();
    harness.editor.view.hasFocus = vi.fn(() => false);
    harness.emit("blur");
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.getByRole("toolbar", { name: "Edit table" })).toBeVisible();

    button.blur();
    harness.emit("blur");
    await waitFor(() =>
      expect(screen.queryByRole("toolbar", { name: "Edit table" })).toBeNull(),
    );
  });
});
