// Site-wide behaviour, loaded on every page (deferred). Currently just the
// confirm-before-submit guard: any form with a data-confirm message must be
// acknowledged before it posts. Kept out of the markup so the page can ship a
// strict script-src CSP (no inline handlers).
document.addEventListener('submit', function (e) {
  var msg = e.target.getAttribute && e.target.getAttribute('data-confirm');
  if (msg && !window.confirm(msg)) e.preventDefault();
}, true);

(function setupPrintDetails() {
  const openForPrintingClass = 'openForPrinting';

  function openDetailsForPrint() {
    for (const details of document.querySelectorAll('details:not([open])')) {
      details.setAttribute('open', '');
      details.classList.add(openForPrintingClass);
    }
  }

  function closeDetailsAfterPrint() {
    for (const details of document.querySelectorAll(`details.${openForPrintingClass}`)) {
      details.removeAttribute('open');
      details.classList.remove(openForPrintingClass);
    }
  }

  window.addEventListener('beforeprint', openDetailsForPrint);
  window.addEventListener('afterprint', closeDetailsAfterPrint);
})();
