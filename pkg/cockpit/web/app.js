// shadowtap Cockpit Frontend Application
(function () {
  let isStreamPaused = false;
  let activeFilter = 'all'; // 'all', 'regressed', 'status_drift', 'type_drift', 'missing_key'
  let searchQuery = '';
  let transactions = [];
  let selectedTxId = null;
  let eventSource = null;

  // DOM Elements
  const globalSRRElem = document.getElementById('global-srr-val');
  const totalShadowedElem = document.getElementById('total-shadowed-val');
  const totalRegressedElem = document.getElementById('total-regressed-val');
  const queueDepthElem = document.getElementById('queue-depth-val');
  const pauseStreamBtn = document.getElementById('pause-stream-btn');
  const exportNdjsonBtn = document.getElementById('export-ndjson-btn');
  const routesTbody = document.getElementById('routes-tbody');
  const routesSummary = document.getElementById('routes-summary-text');
  const txListElem = document.getElementById('transactions-list');
  const searchInput = document.getElementById('filter-search');
  const toastElem = document.getElementById('toast-notify');

  // Diff Pane Elements
  const diffTitle = document.getElementById('diff-title');
  const diffSubtitle = document.getElementById('diff-subtitle');
  const diffMetaTags = document.getElementById('diff-meta-tags');
  const mismatchesContainer = document.getElementById('mismatches-container');
  const mismatchesCount = document.getElementById('mismatches-count');
  const mismatchesChips = document.getElementById('mismatches-chips');
  const primaryPaneStats = document.getElementById('primary-pane-stats');
  const shadowPaneStats = document.getElementById('shadow-pane-stats');
  const primaryCodeView = document.getElementById('primary-code-view');
  const shadowCodeView = document.getElementById('shadow-code-view');

  // Initialize
  function init() {
    setupEventListeners();
    fetchStats();
    fetchRoutes();
    fetchInitialPairs();
    startEventStream();

    setInterval(fetchStats, 2000);
    setInterval(fetchRoutes, 3000);
  }

  function setupEventListeners() {
    pauseStreamBtn.addEventListener('click', () => {
      isStreamPaused = !isStreamPaused;
      pauseStreamBtn.textContent = isStreamPaused ? 'Resume Stream' : 'Pause Stream';
      pauseStreamBtn.classList.toggle('btn-secondary', !isStreamPaused);
      pauseStreamBtn.style.background = isStreamPaused ? '#f59e0b' : '';
      pauseStreamBtn.style.color = isStreamPaused ? '#000' : '';
    });

    if (exportNdjsonBtn) {
      exportNdjsonBtn.addEventListener('click', () => {
        showToast('Exporting captured transactions to NDJSON...');
        window.location.href = '/api/export/ndjson';
      });
    }

    // Filter tab buttons
    document.querySelectorAll('.filter-tab-bar .tab-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        document.querySelectorAll('.filter-tab-bar .tab-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        activeFilter = btn.getAttribute('data-filter');
        renderTransactions();
      });
    });

    searchInput.addEventListener('input', (e) => {
      searchQuery = e.target.value.toLowerCase().trim();
      renderTransactions();
    });
  }

  function showToast(msg) {
    if (!toastElem) return;
    toastElem.textContent = msg;
    toastElem.style.display = 'block';
    setTimeout(() => {
      toastElem.style.display = 'none';
    }, 3500);
  }

  // Fetch Summary Stats
  async function fetchStats() {
    try {
      const res = await fetch('/api/stats');
      if (!res.ok) return;
      const data = await res.json();
      globalSRRElem.textContent = (data.global_srr * 100).toFixed(2) + '%';
      totalShadowedElem.textContent = data.total_shadowed.toLocaleString();
      totalRegressedElem.textContent = data.total_regressed.toLocaleString();
      queueDepthElem.textContent = `${data.queue_depth} / 5000`;
    } catch (err) {
      console.warn('Failed to fetch stats:', err);
    }
  }

  // Fetch Route Scorecard
  async function fetchRoutes() {
    try {
      const res = await fetch('/api/routes');
      if (!res.ok) return;
      const routes = await res.json();
      renderRoutes(routes);
    } catch (err) {
      console.warn('Failed to fetch routes:', err);
    }
  }

  function renderRoutes(routes) {
    if (!routes || routes.length === 0) {
      routesTbody.innerHTML = '<tr><td colspan="7" class="empty-state">Awaiting inbound shadowed traffic...</td></tr>';
      routesSummary.textContent = '0 active routes monitored';
      return;
    }

    routesSummary.textContent = `${routes.length} active routes monitored`;
    routesTbody.innerHTML = routes.map(r => {
      const srrPct = (r.srr * 100).toFixed(2);
      const isHealthy = r.srr <= 0.01;
      const deltaMs = r.latency_delta_p99_ms.toFixed(1);
      const deltaSign = r.latency_delta_p99_ms > 0 ? `+${deltaMs}ms` : `${deltaMs}ms`;

      // Sparkline latency visual calculation
      const maxLatency = Math.max(r.primary_latency_p99_ms, r.shadow_latency_p99_ms, 1.0);
      const primPct = Math.min(Math.round((r.primary_latency_p99_ms / maxLatency) * 100), 100);
      const shadPct = Math.min(Math.round((r.shadow_latency_p99_ms / maxLatency) * 100), 100);

      return `
        <tr>
          <td><span class="route-badge">${escapeHtml(r.route)}</span></td>
          <td>${r.total_requests.toLocaleString()}</td>
          <td><strong class="${r.srr > 0 ? 'text-danger' : 'text-success'}">${srrPct}%</strong></td>
          <td>${r.primary_latency_p99_ms.toFixed(1)}ms</td>
          <td>${r.shadow_latency_p99_ms.toFixed(1)}ms</td>
          <td>
            <div class="latency-bar-container">
              <div class="latency-bar-wrapper" title="Primary: ${r.primary_latency_p99_ms.toFixed(1)}ms">
                <div class="bar-prim" style="width: ${primPct}%;"></div>
              </div>
              <div class="latency-bar-wrapper" title="Shadow: ${r.shadow_latency_p99_ms.toFixed(1)}ms">
                <div class="bar-shad" style="width: ${shadPct}%;"></div>
              </div>
            </div>
          </td>
          <td>
            <span class="status-badge ${isHealthy ? 'badge-healthy' : 'badge-danger'}">
              ${isHealthy ? 'HEALTHY' : 'REGRESSED'}
            </span>
          </td>
        </tr>
      `;
    }).join('');
  }

  // Fetch Historical Pairs
  async function fetchInitialPairs() {
    try {
      const res = await fetch('/api/pairs?limit=50');
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        transactions = data;
        renderTransactions();
        if (transactions.length > 0 && !selectedTxId) {
          selectTransaction(transactions[0]);
        }
      }
    } catch (err) {
      console.warn('Failed to fetch pairs:', err);
    }
  }

  // Server-Sent Events Live Stream
  function startEventStream() {
    if (eventSource) {
      eventSource.close();
    }
    eventSource = new EventSource('/api/events/live');

    eventSource.onmessage = (event) => {
      if (isStreamPaused) return;
      try {
        const pair = JSON.parse(event.data);
        transactions.unshift(pair);
        if (transactions.length > 200) {
          transactions.pop();
        }
        renderTransactions();
        if (!selectedTxId) {
          selectTransaction(pair);
        }
      } catch (err) {
        console.error('Error parsing SSE event:', err);
      }
    };

    eventSource.onerror = () => {
      console.warn('SSE stream error, retrying...');
    };
  }

  // Render Transaction List
  function renderTransactions() {
    const filtered = transactions.filter(t => {
      // Filter by tab
      if (activeFilter === 'regressed' && t.diff.is_match) return false;
      if (activeFilter === 'status_drift' && !t.diff.status_drift) return false;
      if (activeFilter === 'type_drift') {
        const hasType = (t.diff.mismatches || []).some(m => m.kind === 'TYPE_DRIFT');
        if (!hasType) return false;
      }
      if (activeFilter === 'missing_key') {
        const hasMissing = (t.diff.mismatches || []).some(m => m.kind === 'MISSING_KEY');
        if (!hasMissing) return false;
      }

      // Filter by search query
      if (searchQuery) {
        const matchPath = t.path && t.path.toLowerCase().includes(searchQuery);
        const matchMethod = t.method && t.method.toLowerCase().includes(searchQuery);
        const matchID = t.id && t.id.toLowerCase().includes(searchQuery);
        if (!matchPath && !matchMethod && !matchID) return false;
      }
      return true;
    });

    if (filtered.length === 0) {
      txListElem.innerHTML = '<div class="empty-state">No matching transactions found</div>';
      return;
    }

    txListElem.innerHTML = filtered.map(t => {
      const isRegressed = !t.diff.is_match;
      const isSelected = t.id === selectedTxId;
      const delta = t.diff.latency_delta_ms > 0 ? `+${t.diff.latency_delta_ms.toFixed(1)}ms` : `${t.diff.latency_delta_ms.toFixed(1)}ms`;
      const timeStr = new Date(t.timestamp).toLocaleTimeString();

      return `
        <div class="tx-card ${isSelected ? 'active' : ''}" data-id="${t.id}">
          <div class="tx-top">
            <div class="tx-method-path">
              <span class="method-pill method-${t.method}">${t.method}</span>
              <span class="tx-path" title="${escapeHtml(t.path)}">${escapeHtml(t.path)}</span>
            </div>
            <span class="tx-match-tag ${isRegressed ? 'tag-regress' : 'tag-match'}">
              ${isRegressed ? 'DRIFT' : 'MATCH'}
            </span>
          </div>
          <div class="tx-bottom">
            <span class="tx-time">${timeStr} (${t.id})</span>
            <span class="tx-delta">Δ ${delta}</span>
          </div>
        </div>
      `;
    }).join('');

    // Attach click handlers
    txListElem.querySelectorAll('.tx-card').forEach(card => {
      card.addEventListener('click', () => {
        const id = card.getAttribute('data-id');
        const found = transactions.find(t => t.id === id);
        if (found) {
          selectTransaction(found);
        }
      });
    });
  }

  // Select Transaction & Populate Diff Inspector
  function selectTransaction(t) {
    selectedTxId = t.id;

    // Highlight card
    txListElem.querySelectorAll('.tx-card').forEach(card => {
      card.classList.toggle('active', card.getAttribute('data-id') === t.id);
    });

    diffTitle.textContent = `${t.method} ${t.path}`;
    diffSubtitle.textContent = `Transaction ID: ${t.id} | Timestamp: ${new Date(t.timestamp).toISOString()}`;

    // Status tags
    const isMatch = t.diff.is_match;
    const distance = (t.diff.semantic_distance * 100).toFixed(1);
    diffMetaTags.innerHTML = `
      <span class="status-badge ${isMatch ? 'badge-healthy' : 'badge-danger'}">
        ${isMatch ? 'SEMANTIC MATCH' : 'SEMANTIC REGRESSION'}
      </span>
      <span class="metric-pill" style="padding: 3px 8px; font-size: 0.72rem;">
        DISTANCE: ${distance}%
      </span>
    `;

    // Discrepancies Chips with One-Click Ignore Buttons
    const mismatches = t.diff.mismatches || [];
    if (mismatches.length > 0) {
      mismatchesContainer.style.display = 'block';
      mismatchesCount.textContent = mismatches.length;
      mismatchesChips.innerHTML = mismatches.map(m => {
        const canIgnore = m.path && !m.path.startsWith('_');
        return `
          <div class="chip">
            <span class="chip-kind ${m.kind}">${m.kind}</span>
            <span>${escapeHtml(m.path)}: ${escapeHtml(m.message)}</span>
            ${canIgnore ? `<button class="btn-chip-ignore" data-path="${escapeHtml(m.path)}" title="Ignore this field dynamically in real time">+ Ignore</button>` : ''}
          </div>
        `;
      }).join('');

      // Attach ignore listeners
      mismatchesChips.querySelectorAll('.btn-chip-ignore').forEach(btn => {
        btn.addEventListener('click', async (e) => {
          e.stopPropagation();
          const path = btn.getAttribute('data-path');
          try {
            const res = await fetch('/api/mask/ignore', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ path: path })
            });
            if (res.ok) {
              btn.textContent = 'Ignored!';
              btn.disabled = true;
              showToast(`Field pattern '${path}' added to active mask rules.`);
            }
          } catch (err) {
            console.error('Failed to add ignore pattern:', err);
          }
        });
      });
    } else {
      mismatchesContainer.style.display = 'none';
    }

    // Update Panes Stats
    primaryPaneStats.textContent = `HTTP ${t.primary.status_code} | ${t.primary.latency_ms.toFixed(1)}ms`;
    shadowPaneStats.textContent = `HTTP ${t.shadow.status_code} | ${t.shadow.latency_ms.toFixed(1)}ms`;

    // Line-by-line colored diff rendering
    const primRaw = t.diff.primary_clean_json || t.primary.body || '// No payload body';
    const shadRaw = t.diff.shadow_clean_json || t.shadow.body || '// No payload body';

    primaryCodeView.innerHTML = renderHighlightedLines(primRaw, mismatches, 'primary');
    shadowCodeView.innerHTML = renderHighlightedLines(shadRaw, mismatches, 'shadow');
  }

  // Render code lines with visual highlight classes
  function renderHighlightedLines(jsonText, mismatches, side) {
    if (!jsonText) return '';
    const lines = jsonText.split('\n');

    return lines.map(line => {
      let lineClass = '';

      for (const m of mismatches) {
        if (!m.path || m.path.startsWith('_')) continue;
        // Extract leaf key name
        const parts = m.path.split('.');
        const lastPart = parts[parts.length - 1].replace(/\[\d+\]/g, '');

        if (lastPart && line.includes(`"${lastPart}"`)) {
          if (m.kind === 'MISSING_KEY') {
            if (side === 'primary') lineClass = 'line-del';
          } else if (m.kind === 'UNEXPECTED_KEY') {
            if (side === 'shadow') lineClass = 'line-add';
          } else if (m.kind === 'TYPE_DRIFT') {
            lineClass = 'line-drift';
          } else if (m.kind === 'VALUE_DRIFT') {
            lineClass = side === 'primary' ? 'line-del' : 'line-add';
          }
          break;
        }
      }

      return `<div class="diff-line ${lineClass}">${escapeHtml(line)}</div>`;
    }).join('');
  }

  function escapeHtml(str) {
    if (!str) return '';
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  window.addEventListener('DOMContentLoaded', init);
})();
