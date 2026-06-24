(function () {
	const apiPrefix = document.querySelector('meta[name="api-prefix"]')?.content || "/api/v1";
	const bar = document.getElementById("bar");
	const streamPath = bar?.dataset.streamPath || "/sync/stream";
	const eventStream = new EventSource(apiPrefix + streamPath);
	const label = document.getElementById("label");
	const error = document.getElementById("error");
	const errorActions = document.getElementById("sync-error-actions");
	const successActions = document.getElementById("sync-success-actions");

	/** @param {{total: number, step: number}} data*/
	function percentage(data) {
		return data.total ? Math.round((data.step / data.total) * 100) : 0;
	}

	eventStream.addEventListener("progress", function (event) {
		const data = JSON.parse(event.data);
		bar.style.width = percentage(data) + "%";
		label.textContent =
			"Fetching " + data.name + "… (" + data.step + "/" + data.total + ")";
		label.classList.remove("hidden");
		error.classList.add("hidden");
	});

	eventStream.addEventListener("done", function (event) {
		bar.style.width = "100%";
		label.textContent = "Done!";
		label.classList.remove("hidden");
		error.classList.add("hidden");
		try {
			if (JSON.parse(event.data).matched) {
				document.getElementById("signup-action").classList.add("hidden");
				document.getElementById("signin-action").classList.remove("hidden");
			}
		} catch (_) {}
		successActions.classList.remove("hidden");
		errorActions.classList.add("hidden");
		eventStream.close();
	});

	eventStream.addEventListener("error", function (e) {
		// A server-sent error carries a message (e.g. a cooldown); a bare connection
		// error means the sync is gone — finished and swept, claimed, or expired —
		// which is what a refresh of a stale syncing page hits. Either way, offer a
		// way to start over instead of leaving the user stuck.
		let msg;
		try {
			msg = JSON.parse(e.data).error;
		} catch (_) {}
		error.textContent =
			msg ||
			"This sync is no longer active — it may have finished, expired, or the connection dropped. You can start a new one below.";
		label.classList.add("hidden");
		error.classList.remove("hidden");
		successActions.classList.add("hidden");
		errorActions.classList.remove("hidden");
		eventStream.close();
	});
})();
