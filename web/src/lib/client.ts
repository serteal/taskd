import { createClient, type Client } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AdminService } from "../gen/admin/admin_pb";
import { TaskService } from "../gen/task/task_pb";

export type TaskClient = Client<typeof TaskService>;
export type AdminClient = Client<typeof AdminService>;

// Same-origin: Vite proxies /task.TaskService in dev; taskd embeds the
// bundle in prod. Nothing here ever needs CORS.
export function newTaskClient(): TaskClient {
  return createClient(TaskService, createConnectTransport({ baseUrl: "/" }));
}

// AdminService (extension enable/disable, later daemon settings) lives on the
// same origin as TaskService — the Settings UI calls it the same way.
export function newAdminClient(): AdminClient {
  return createClient(AdminService, createConnectTransport({ baseUrl: "/" }));
}
