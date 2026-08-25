-- Carries the ACP tool call id and terminal status through to the
-- transcript. Without call_id, a result is paired to its call by
-- tool-name FIFO (see build-steps.ts), which misattributes a result
-- whenever two same-named calls are in flight out of order. Without
-- status, a failed tool call renders as an ordinary successful one
-- once its (late-arriving) result lands.
--
-- No index: nothing queries by either column, both are read alongside
-- the row they already belong to.
ALTER TABLE task_message ADD COLUMN IF NOT EXISTS call_id TEXT;
ALTER TABLE task_message ADD COLUMN IF NOT EXISTS status TEXT;
