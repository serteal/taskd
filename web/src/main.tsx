import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App";
import { newTaskClient } from "./lib/client";
import { TaskStore } from "./lib/store";
import { StoreContext } from "./lib/hooks";

const store = new TaskStore(newTaskClient());

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <StoreContext.Provider value={store}>
      <App />
    </StoreContext.Provider>
  </StrictMode>,
);
