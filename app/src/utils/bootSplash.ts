// Hides the static index.html boot splash (#loading-screen) once a React
// root has mounted enough to take over the screen. Shared by App and any
// other window entry (e.g. PresentRoot) so no window gets stuck showing the
// splash forever.
export function hideBootSplash() {
  const loadingScreen = document.getElementById('loading-screen');
  if (loadingScreen) {
    loadingScreen.classList.add('hidden');
    // Remove from DOM after transition completes
    setTimeout(() => loadingScreen.remove(), 300);
  }
}
