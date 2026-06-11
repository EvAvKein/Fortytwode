(function () {
  var es = new EventSource('/api/fetch/stream');
  var bar = document.getElementById('bar');
  var label = document.getElementById('label');
  function pct(d) { return d.total ? Math.round(d.step / d.total * 100) : 0; }
  es.addEventListener('progress', function (e) {
    var d = JSON.parse(e.data);
    bar.style.width = pct(d) + '%';
    label.textContent = 'Fetching ' + d.name + '… (' + d.step + '/' + d.total + ')';
  });
  es.addEventListener('done', function (e) {
    bar.style.width = '100%';
    label.textContent = 'Done!';
    var matched = false;
    try { matched = JSON.parse(e.data).matched; } catch (_) {}
    if (matched) {
      document.getElementById('signup-action').classList.add('hidden');
      document.getElementById('signin-action').classList.remove('hidden');
    }
    document.getElementById('actions').classList.remove('hidden');
    es.close();
  });
  es.addEventListener('error', function (e) {
    // A server-sent error carries a message (e.g. a cooldown); a bare connection
    // error means the sync is gone — finished and swept, claimed, or expired —
    // which is what a refresh of a stale syncing page hits. Either way, offer a
    // way to start over instead of leaving the user stuck.
    var msg;
    try { msg = JSON.parse(e.data).error; } catch (_) {}
    var el = document.getElementById('error');
    el.textContent = msg || 'This sync is no longer active — it may have finished, expired, or the connection dropped. You can start a new one below.';
    el.classList.remove('hidden');
    document.getElementById('error-actions').classList.remove('hidden');
    es.close();
  });
})();
