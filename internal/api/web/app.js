(() => {
  const $ = (selector) => document.querySelector(selector);
  const state = { deployments: [], namespace: 'demo', token: localStorage.getItem('deploypilot-token') || '', timer: null };
  const els = {
    loading: $('#loading-state'), empty: $('#empty-state'), table: $('#table-wrap'), rows: $('#deployment-rows'),
    error: $('#error-banner'), namespace: $('#namespace-filter'), total: $('#total-count'), healthy: $('#healthy-count'),
    progressing: $('#progressing-count'), failed: $('#failed-count'), navCount: $('#nav-count'), resultCount: $('#result-count'),
    updated: $('#last-updated'), cluster: $('#cluster-label'), createDialog: $('#create-dialog'), form: $('#create-form'), formError: $('#form-error'),
    submit: $('#submit-button'), details: $('#detail-dialog'), detailContent: $('#detail-content'), tokenDialog: $('#token-dialog'), tokenForm: $('#token-form'), tokenInput: $('#token-input'), toast: $('#toast')
  };

  const esc = (value) => String(value ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
  const date = (value) => { if (!value) return '—'; const d = new Date(value); return Number.isNaN(d.valueOf()) ? '—' : d.toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }); };
  const condition = (d) => (d.status?.conditions || []).find((c) => c.type === 'Failed') || (d.status?.conditions || []).find((c) => c.type === 'Available') || (d.status?.conditions || []).find((c) => c.type === 'Progressing');
  const statusOf = (d) => { const c = condition(d); if (!c) return 'Pending'; if (c.type === 'Failed' && c.status === 'True') return 'Failed'; if (c.type === 'Available' && c.status === 'True') return 'Healthy'; return 'Progressing'; };
  const toast = (message, isError = false) => { els.toast.textContent = message; els.toast.className = `toast show${isError ? ' error' : ''}`; clearTimeout(toast.timer); toast.timer = setTimeout(() => { els.toast.className = 'toast'; }, 3200); };

  async function request(path, options = {}) {
    const headers = new Headers(options.headers || {});
    if (state.token) headers.set('Authorization', `Bearer ${state.token}`);
    if (options.body) headers.set('Content-Type', 'application/json');
    const response = await fetch(path, { ...options, headers });
    const body = response.status === 204 ? null : await response.json().catch(() => null);
    if (response.status === 401) { openTokenDialog(); throw new Error('Enter the API token to connect.'); }
    if (!response.ok) {
      const err = body?.error;
      const fieldText = err?.fields ? Object.entries(err.fields).map(([k, v]) => `${k}: ${v}`).join(' · ') : '';
      throw new Error([err?.message || `Request failed (${response.status})`, fieldText].filter(Boolean).join(' — '));
    }
    return body;
  }

  async function refresh() {
    els.loading.hidden = false; els.empty.hidden = true; els.table.hidden = true; els.error.hidden = true;
    const namespace = els.namespace.value.trim() || 'demo'; state.namespace = namespace;
    try {
      const data = await request(`/v1/deployments?namespace=${encodeURIComponent(namespace)}&limit=100`);
      state.deployments = data.items || [];
      render(); els.cluster.textContent = 'API connected'; els.updated.textContent = 'Updated just now';
    } catch (error) {
      els.error.textContent = error.message; els.error.hidden = false; els.cluster.textContent = error.message.includes('token') ? 'Authentication needed' : 'Connection issue';
      els.total.textContent = '—'; els.healthy.textContent = '—'; els.progressing.textContent = '—'; els.failed.textContent = '—'; els.navCount.textContent = '—';
    } finally { els.loading.hidden = true; }
  }

  function render() {
    const items = state.deployments;
    const counts = { Healthy: 0, Progressing: 0, Failed: 0, Pending: 0 };
    items.forEach((d) => { counts[statusOf(d)]++; });
    els.total.textContent = items.length; els.healthy.textContent = counts.Healthy; els.progressing.textContent = counts.Progressing + counts.Pending; els.failed.textContent = counts.Failed;
    els.navCount.textContent = items.length; els.resultCount.textContent = `Showing ${items.length} deployment${items.length === 1 ? '' : 's'}`;
    els.empty.hidden = items.length !== 0; els.table.hidden = items.length === 0;
    els.rows.innerHTML = items.map((d) => {
      const status = statusOf(d); const css = status.toLowerCase(); const ready = `${d.status?.readyReplicas || 0} / ${d.status?.desiredReplicas ?? d.spec?.replicas ?? 1}`;
      return `<tr><td><button class="deployment-name" data-detail="${esc(d.metadata?.name)}">${esc(d.metadata?.name)}</button><div class="subcell">${esc(d.metadata?.namespace)}</div></td><td><span class="status-pill status-${css}">${esc(status)}</span></td><td>${esc(ready)}</td><td class="image-cell" title="${esc(d.spec?.image)}">${esc(d.spec?.image)}</td><td>${esc(date(d.metadata?.creationTimestamp))}</td><td><div class="row-actions"><button class="row-action" data-detail="${esc(d.metadata?.name)}">Details</button><button class="row-action delete" data-delete="${esc(d.metadata?.name)}">Delete</button></div></td></tr>`;
    }).join('');
  }

  function openTokenDialog() { els.tokenInput.value = state.token; els.tokenDialog.showModal(); }
  function openCreateDialog() { els.form.reset(); els.form.elements.namespace.value = els.namespace.value.trim() || 'demo'; els.form.elements.port.value = '8080'; els.form.elements.replicas.value = '1'; els.formError.hidden = true; els.createDialog.showModal(); }
  function showDetails(name) {
    const d = state.deployments.find((item) => item.metadata?.name === name); if (!d) return;
    const c = condition(d); const status = statusOf(d);
    els.detailContent.innerHTML = `<div class="detail-heading"><div><div class="eyebrow">DEPLOYMENT REQUEST</div><h2>${esc(name)}</h2><div class="detail-subtitle">${esc(d.metadata?.namespace)} · ${esc(d.spec?.image)}</div></div><button class="close-button" type="button" data-close aria-label="Close">×</button></div><div class="detail-grid"><div class="detail-item"><small>Status</small><strong><span class="status-pill status-${status.toLowerCase()}">${esc(status)}</span></strong></div><div class="detail-item"><small>Ready replicas</small><strong>${esc(d.status?.readyReplicas || 0)} / ${esc(d.status?.desiredReplicas ?? d.spec?.replicas ?? 1)}</strong></div><div class="detail-item"><small>Container port</small><strong>${esc(d.spec?.port || 8080)}</strong></div><div class="detail-item"><small>Created</small><strong>${esc(date(d.metadata?.creationTimestamp))}</strong></div><div class="detail-item"><small>Managed Deployment</small><strong>${esc(d.status?.deploymentName || name)}</strong></div><div class="detail-item"><small>Managed Service</small><strong>${esc(d.status?.serviceName || name)}</strong></div></div>${c ? `<div class="detail-condition"><strong>${esc(c.type)} · ${esc(c.reason || '')}</strong><p>${esc(c.message || 'No additional status message.')}</p></div>` : '<div class="detail-condition"><strong>Waiting for controller</strong><p>The request is saved. The controller will report progress shortly.</p></div>'}`;
    els.details.showModal();
  }

  $('#new-deployment-button').addEventListener('click', openCreateDialog);
  document.querySelectorAll('[data-create]').forEach((button) => button.addEventListener('click', openCreateDialog));
  $('#refresh-button').addEventListener('click', refresh); $('#filter-button').addEventListener('click', refresh);
  $('#namespace-filter').addEventListener('keydown', (event) => { if (event.key === 'Enter') refresh(); });
  $('#settings-button').addEventListener('click', openTokenDialog);
  document.addEventListener('click', async (event) => {
    const detail = event.target.closest('[data-detail]'); if (detail) showDetails(detail.dataset.detail);
    const del = event.target.closest('[data-delete]'); if (del) {
      const name = del.dataset.delete; if (!confirm(`Delete deployment request “${name}” and its managed workload?`)) return;
      try { await request(`/v1/deployments/${encodeURIComponent(name)}?namespace=${encodeURIComponent(state.namespace)}`, { method: 'DELETE' }); toast(`Deletion requested for ${name}`); await refresh(); }
      catch (error) { toast(error.message, true); }
    }
    if (event.target.closest('[data-close]')) event.target.closest('dialog')?.close();
  });

  els.form.addEventListener('submit', async (event) => {
    event.preventDefault(); els.formError.hidden = true;
    const data = new FormData(els.form); const spec = { name: data.get('name').trim(), namespace: data.get('namespace').trim(), image: data.get('image').trim(), replicas: Number(data.get('replicas') || 1), port: Number(data.get('port') || 8080) };
    const resources = { requests: {}, limits: {} };
    for (const [key, field] of [['cpuRequest', 'cpu'], ['memoryRequest', 'memory']]) if (data.get(key).trim()) resources.requests[field] = data.get(key).trim();
    for (const [key, field] of [['cpuLimit', 'cpu'], ['memoryLimit', 'memory']]) if (data.get(key).trim()) resources.limits[field] = data.get(key).trim();
    if (Object.keys(resources.requests).length || Object.keys(resources.limits).length) spec.resources = resources;
    const path = data.get('readinessPath').trim(); if (path) spec.readinessProbe = { path };
    els.submit.disabled = true; els.submit.textContent = 'Creating…';
    try {
      await request('/v1/deployments', { method: 'POST', body: JSON.stringify(spec) }); els.createDialog.close(); els.namespace.value = spec.namespace; toast(`Deployment request “${spec.name}” created`); await refresh();
    } catch (error) { els.formError.textContent = error.message; els.formError.hidden = false; }
    finally { els.submit.disabled = false; els.submit.innerHTML = 'Create deployment <span>→</span>'; }
  });

  els.tokenForm.addEventListener('submit', (event) => { event.preventDefault(); state.token = els.tokenInput.value.trim(); localStorage.setItem('deploypilot-token', state.token); els.tokenDialog.close(); refresh(); });
  refresh(); state.timer = setInterval(refresh, 10000);
})();
