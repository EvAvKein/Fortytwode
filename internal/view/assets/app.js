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

// Render each server-sent UTC sync timestamp in the viewer's local timezone.
// The element ships empty; the server only provides the machine-readable
// datetime attribute, so this fills in the visible text.
(function localizeSyncTime() {
	for (const el of document.querySelectorAll("time[data-synced]")) {
		const iso = el.getAttribute("datetime");
		if (!iso) continue;
		const d = new Date(iso);
		if (isNaN(d.getTime())) continue;
		const now = new Date();
		const sameDay =
			d.getFullYear() === now.getFullYear() &&
			d.getMonth() === now.getMonth() &&
			d.getDate() === now.getDate();
		el.textContent = sameDay
			? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
			: d.toLocaleDateString();
	}
})();

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
					const contentType = response.headers.get("Content-Type") || "";
					if (contentType.indexOf("text/html") !== -1) {
						// Server rendered a full styled page (login, validation, rate-limit,
						// 404…); show it in place rather than dumping its text into a popup.
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

// Navigating to 42's authorize page can take several seconds once the browser
// actually leaves this document, and that wait is on 42's side (our own
// /api/v1/sync handler does no work before redirecting).
// Swap the trigger for a status notice instead.
// This only mutates the DOM after the click; navigation itself proceeds normally.
(function setupSyncNotice() {
	function setSyncNoticeVisible(visible) {
		for (const el of document.querySelectorAll("[data-sync-hide]")) {
			el.classList.toggle("hidden", visible);
		}
		for (const el of document.querySelectorAll("[data-sync-notice]")) {
			el.classList.toggle("hidden", !visible);
		}
	}

	document.addEventListener("click", function showSyncNotice(e) {
		if (e.target.closest("[data-sync-trigger]")) setSyncNoticeVisible(true);
	});

	// If the browser restores this page from bfcache (e.g. the user hits Back
	// from 42's site), it would come back exactly as left: trigger hidden, notice
	// stuck showing, no way to retry. This undoes the above mutation in that case.
	window.addEventListener("pageshow", function resetSyncNoticeOnRestore(e) {
		if (e.persisted) setSyncNoticeVisible(false);
	});
})();
