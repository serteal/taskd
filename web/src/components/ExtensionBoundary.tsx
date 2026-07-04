import { Component, type ReactNode } from "react";
import { notify } from "../lib/notify";

// Isolates an extension-rendered surface (panel, view, or detail section): a
// render error shows a small fallback and a toast instead of white-screening
// the whole app. One extension's bug can't take the others — or core — down.
export class ExtensionBoundary extends Component<
  { name: string; children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch(err: unknown) {
    notify.error(`Extension “${this.props.name}” hit an error and was disabled here.`);
    console.error(`taskd: extension ${this.props.name} render error:`, err);
  }

  render() {
    if (this.state.failed) {
      return (
        <div className="p-4 text-center text-[12px] text-mute">
          This extension surface failed to render.
        </div>
      );
    }
    return this.props.children;
  }
}
