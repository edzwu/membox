/* Tiny pub/sub used to break import cycles: feature modules publish state
   changes, and the chrome composition layer subscribes. Without it, every
   module that mutates session state would have to import the modules that
   own the chrome — and those chrome modules already import the features. */

const listeners = new Map();

export function on(event, listener) {
  if (!listeners.has(event)) listeners.set(event, new Set());
  listeners.get(event).add(listener);
  return () => listeners.get(event)?.delete(listener);
}

export function emit(event, ...args) {
  for (const listener of listeners.get(event) || []) listener(...args);
}

// Session state changed in a way the chrome should reflect (connection,
// dirty flag, document identity, save progress).
const RENDER = 'membox:render';

export function onRender(listener) {
  on(RENDER, listener);
}

export function emitRender() {
  emit(RENDER);
}
