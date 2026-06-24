// Site-wide behaviour, loaded on every page (deferred). Currently just the
// confirm-before-submit guard: any form with a data-confirm message must be
// acknowledged before it posts. Kept out of the markup so the page can ship a
// strict script-src CSP (no inline handlers).
document.addEventListener(
	"submit",
	function showFormConfirmation(e) {
		const msg = e.target.getAttribute && e.target.getAttribute("data-confirm");
		if (msg && !window.confirm(msg)) e.preventDefault();
	},
	true,
);

(function setupPrintDetails() {
	const openForPrintingClass = "openForPrinting";

	function openDetailsForPrint() {
		for (const details of document.querySelectorAll("details:not([open])")) {
			details.setAttribute("open", "");
			details.classList.add(openForPrintingClass);
		}
	}

	function closeDetailsAfterPrint() {
		for (const details of document.querySelectorAll(
			`details.${openForPrintingClass}`,
		)) {
			details.removeAttribute("open");
			details.classList.remove(openForPrintingClass);
		}
	}

	window.addEventListener("beforeprint", openDetailsForPrint);
	window.addEventListener("afterprint", closeDetailsAfterPrint);
})();

document.addEventListener("click", function printButtonHandler(e) {
	if (e.target.closest(".print-button")) {
		window.print();
	}
});

// Intercept forms that need methods the browser cannot send natively
// (DELETE, PATCH). The form still falls back to POST if JS is disabled.
document.addEventListener(
	"submit",
	function fixFormMethod(e) {
		const form = e.target;
		const method = form.getAttribute && form.getAttribute("data-method");
		if (!method) return;
		if (e.defaultPrevented) return; // e.g. user cancelled a data-confirm dialog
		e.preventDefault();

		fetch(form.action, {
			method: method.toUpperCase(),
			body: new FormData(form),
			credentials: "same-origin",
		})
			.then(function (response) {
				if (response.ok) {
					// Follow redirects manually so the browser URL ends up at the final page.
					if (response.redirected) {
						window.location.href = response.url;
					} else {
						window.location.reload();
					}
					return;
				}
				return response.text().then(function (html) {
					if (response.status === 401 || response.status === 422) {
						// Server rendered the login/validation page; replace the document.
						document.documentElement.innerHTML = html;
					} else {
						const tmp = document.createElement("div");
						tmp.innerHTML = html;
						alert("Error: " + (tmp.textContent || response.statusText));
					}
				});
			})
			.catch(function (err) {
				alert("Network error: " + err);
			});
	},
	true,
);
