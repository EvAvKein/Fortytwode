// Site-wide behaviour, loaded on every page (deferred). Currently just the
// confirm-before-submit guard: any form with a data-confirm message must be
// acknowledged before it posts. Kept out of the markup so the page can ship a
// strict script-src CSP (no inline handlers).
document.addEventListener('submit', function (e) {
  var msg = e.target.getAttribute && e.target.getAttribute('data-confirm');
  if (msg && !window.confirm(msg)) e.preventDefault();
}, true);
