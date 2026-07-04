import { createClient, type Client } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { TaskService } from "../gen/task/task_pb";

export type TaskClient = Client<typeof TaskService>;

// Same-origin: Vite proxies /task.TaskService in dev; taskd embeds the
// bundle in prod. Nothing here ever needs CORS.
export function newTaskClient(): TaskClient {
  return createClient(TaskService, createConnectTransport({ baseUrl: "/" }));
}
