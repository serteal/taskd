// Host-provided react/jsx-runtime for extension bundles (esbuild
// --jsx=automatic emits imports of jsx/jsxs/Fragment).
const J = window.__TASKD_JSX_RUNTIME__;
if (!J) throw new Error("taskd extension loaded outside the taskd web app");
export const jsx = J.jsx;
export const jsxs = J.jsxs;
export const Fragment = J.Fragment;
