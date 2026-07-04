// Re-exports the HOST's React so extension bundles share one instance
// (hooks break across copies). Alias `react` to this file when bundling.
const R = window.__TASKD_REACT__;
if (!R) throw new Error("taskd extension loaded outside the taskd web app");
export default R;
export const useState = R.useState;
export const useEffect = R.useEffect;
export const useLayoutEffect = R.useLayoutEffect;
export const useMemo = R.useMemo;
export const useCallback = R.useCallback;
export const useRef = R.useRef;
export const useContext = R.useContext;
export const useSyncExternalStore = R.useSyncExternalStore;
export const useId = R.useId;
export const createContext = R.createContext;
export const createElement = R.createElement;
export const cloneElement = R.cloneElement;
export const forwardRef = R.forwardRef;
export const memo = R.memo;
export const Fragment = R.Fragment;
export const Component = R.Component;
export const StrictMode = R.StrictMode;
