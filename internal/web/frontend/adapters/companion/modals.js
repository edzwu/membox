/* Modal exclusivity without cross-imports: the notes browser and the related
   /open picker must never be open at once, but neither feature module should
   import the other. Each registers itself here and asks this registry to
   close whatever else is open. */

const modals = [];

// modal: { isOpen(): boolean, close(): void }
export function registerModal(modal) {
  modals.push(modal);
  return modal;
}

export function closeOtherModals(except = null) {
  for (const modal of modals) {
    if (modal !== except && modal.isOpen()) modal.close();
  }
}
