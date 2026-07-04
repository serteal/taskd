// The drag-and-drop contract shared by core and extensions. Core task rows
// are drag sources that put the task id on the dataTransfer under a custom
// mime (plus a text/plain fallback); any drop target — a core list for
// reordering, or an extension panel for timeboxing — reads it back. The mime
// is the entire contract between a drag started in core and a drop handled
// by an extension.

export const TASK_DRAG_MIME = "application/x-taskd-task";

export function setTaskDrag(dt: DataTransfer, id: string): void {
  dt.setData(TASK_DRAG_MIME, id);
  dt.setData("text/plain", id);
  dt.effectAllowed = "move";
}

export function readTaskId(dt: DataTransfer): string | null {
  return dt.getData(TASK_DRAG_MIME) || dt.getData("text/plain") || null;
}
