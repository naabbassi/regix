// Applies the saved theme ("light", "dark", or the system's when unset) before the page paints.
(function () {
  var saved = null;
  try {
    saved = localStorage.getItem('regix-theme');
  } catch (e) {}
  var dark = saved === 'dark' || (saved !== 'light' && matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
})();
