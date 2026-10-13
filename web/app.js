(() => {
  'use strict';

  // ---------------------------------------------------------------
  // Помоћне функције
  // ---------------------------------------------------------------
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];
  const LOCALE = 'sr-Cyrl-RS';
  // Српски формат: 500.000,00 (тачка = хиљаде, зарез = децимале)
  const nf = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 2, useGrouping: 'always' });
  const nf0 = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 0 });
  const nfRate = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 4, useGrouping: 'always' });
  const CUR_SYMBOL = { RSD: 'RSD', EUR: '€', USD: '$' };

  const pad = (n) => String(n).padStart(2, '0');
  const round2 = (v) => Math.round(v * 100) / 100;
  const cap = (s) => s.charAt(0).toUpperCase() + s.slice(1);
  const esc = (s) => String(s || '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  const currentMonth = () => { const d = new Date(); return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`; };
  const today = () => { const d = new Date(); return `${currentMonth()}-${pad(d.getDate())}`; };
  const shiftMonth = (m, delta) => {
    const [y, mo] = m.split('-').map(Number);
    const d = new Date(y, mo - 1 + delta, 1);
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`;
  };
  const parseDate = (s) => { const [y, m, d] = s.split('-').map(Number); return new Date(y, m - 1, d); };
  const daysIn = (m) => { const [y, mo] = m.split('-').map(Number); return new Date(y, mo, 0).getDate(); };

  const monthName = (m, style = 'long') => {
    const [y, mo] = m.split('-').map(Number);
    return new Intl.DateTimeFormat(LOCALE, { month: style }).format(new Date(y, mo - 1, 1)).replace('.', '');
  };
  const monthLabel = (m) => `${cap(monthName(m))} ${m.slice(0, 4)}`;

  function money(v, cur = 'RSD') {
    const n = nf.format(round2(v));
    if (cur === 'RSD') return `${n} RSD`;
    return `${n} ${CUR_SYMBOL[cur] || cur}`;
  }

  function parseAmount(raw) {
    let s = String(raw).trim().replace(/[\s\u00a0\u202f]/g, '');
    if (!s) return NaN;
    if (s.includes(',')) s = s.replace(/\./g, '').replace(',', '.');
    else if (/^\d{1,3}(\.\d{3})+$/.test(s)) s = s.replace(/\./g, '');
    return /^\d*\.?\d+$/.test(s) ? Number(s) : NaN;
  }

  const icons = () => window.lucide && window.lucide.createIcons();

  function plural(n, one, few, many) {
    const m10 = n % 10, m100 = n % 100;
    if (m10 === 1 && m100 !== 11) return one;
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
    return many;
  }
  const troskoviWord = (n) => `${n} ${plural(n, 'трошак', 'трошка', 'трошкова')}`;

  // ---------------------------------------------------------------
  // Стање
  // ---------------------------------------------------------------
  const state = {
    month: currentMonth(),
    data: null,
    history: [],
    rates: null,
    defaultKeshkicVal: 5000,
    selectedKeshkicFilter: 0, // 0 = сви Кешкићи
    editingTx: null,
    editingKeshkic: null,
    lastCurrency: localStorage.getItem('keshk-currency') || 'RSD',
  };

  // ---------------------------------------------------------------
  // API
  // ---------------------------------------------------------------
  class Unauthorized extends Error {}

  async function api(path, { method = 'GET', body } = {}) {
    const res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    if (res.status === 401 && path !== '/api/login') { showLogin(); throw new Unauthorized(); }
    const data = res.status === 204 ? null : await res.json().catch(() => null);
    if (!res.ok) throw new Error((data && data.error) || `Грешка (${res.status})`);
    return data;
  }

  // ---------------------------------------------------------------
  // Обавештења (Toasts) — без емотикона
  // ---------------------------------------------------------------
  function toast(msg, type = 'ok') {
    const el = document.createElement('div');
    el.className = `toast${type === 'error' ? ' is-error' : ''}`;
    const iconName = type === 'error' ? 'alert-triangle' : 'check';
    el.innerHTML = `<i data-lucide="${iconName}"></i><span>${esc(msg)}</span>`;
    $('#toaster').appendChild(el);
    icons();
    setTimeout(() => {
      el.classList.add('is-leaving');
      setTimeout(() => el.remove(), 250);
    }, 2800);
  }

  // ---------------------------------------------------------------
  // Приказ (Views)
  // ---------------------------------------------------------------
  function showLogin() {
    $('#splash').classList.add('is-hidden');
    $('#login-view').hidden = false;
    $('#app-view').hidden = true;
    $('#login-username').focus();
    icons();
  }

  async function showApp(me) {
    if (me && me.defaultKeshkic) state.defaultKeshkicVal = me.defaultKeshkic;
    $('#splash').classList.add('is-hidden');
    $('#login-view').hidden = true;
    $('#app-view').hidden = false;
    await Promise.all([loadRates(), loadHistory()]);
    await load();
  }

  // ---------------------------------------------------------------
  // Подаци
  // ---------------------------------------------------------------
  async function loadRates() {
    try {
      state.rates = await api('/api/rates');
      renderRates();
    } catch {
      state.rates = null;
    }
  }

  function renderRates() {
    if (!state.rates || !state.rates.rates) {
      $('#rate-badge').hidden = true;
      return;
    }
    const eur = state.rates.rates.EUR;
    $('#rate-text').textContent = eur ? `1 € = ${nfRate.format(eur)} RSD` : 'курс…';
    $('#rate-badge').hidden = false;
  }

  async function loadHistory() {
    try {
      state.history = await api(`/api/history?end=${state.month}&n=6`);
    } catch {
      state.history = [];
    }
  }

  async function load() {
    try {
      const data = await api(`/api/months/${state.month}${state.selectedKeshkicFilter ? `?keshkicId=${state.selectedKeshkicFilter}` : ''}`);
      state.data = data;
      render();
    } catch (e) {
      if (!(e instanceof Unauthorized)) toast(e.message, 'error');
    }
  }

  async function goMonth(m) {
    state.month = m;
    state.selectedKeshkicFilter = 0;
    await Promise.all([load(), loadHistory()]);
  }

  // ---------------------------------------------------------------
  // Рендеровање
  // ---------------------------------------------------------------
  function render() {
    if (!state.data) return;
    const s = state.data.summary;
    const cm = currentMonth();

    $('#month-text').textContent = monthLabel(state.month);
    $('#month-label').classList.toggle('is-current', state.month === cm);

    // Херо преглед
    animateNumber($('#balance-value'), s.balance);
    $('#hero').classList.toggle('is-negative', s.balance < 0);
    $('#balance-hint').textContent = balanceHint(s, cm);

    const pct = s.totalKeshkic > 0 ? (s.expenses / s.totalKeshkic) * 100 : (s.expenses > 0 ? 100 : 0);
    const bar = $('#progress-bar');
    bar.style.width = `${Math.min(100, pct)}%`;
    bar.classList.toggle('is-warn', pct >= 80 && pct < 100);
    bar.classList.toggle('is-danger', pct >= 100);
    $('#progress').setAttribute('aria-valuenow', String(Math.round(Math.min(100, pct))));
    $('#progress-spent').textContent = `Потрошено ${nf0.format(pct)}% (${money(s.expenses)})`;
    $('#progress-total').textContent = `од укупно ${money(s.totalKeshkic)}`;

    renderKeshkici();
    renderTransactions();
    renderHistory();
    icons();
  }

  function balanceHint(s, cm) {
    if (!s.keshkici.length) return 'Овај месец још нема Кешкића — кликни на „Нова” или „Додај Кешкић”! ';
    if (s.balance < 0) return `Прекорачење буџета за ${money(-s.balance)}`;
    if (state.month === cm) {
      const d = new Date();
      const left = daysIn(cm) - d.getDate() + 1;
      return `≈ ${money(s.balance / left)} дневно · још ${left} ${plural(left, 'дан', 'дана', 'дана')}`;
    }
    if (state.month < cm) return s.balance > 0 ? `Остало ти је ${money(s.balance)} на крају месеца` : 'Месец је завршен';
    return 'Будући месец';
  }

  function animateNumber(el, to) {
    const from = Number(el.dataset.value || 0);
    el.dataset.value = String(to);
    const start = performance.now();
    const dur = 600;
    const step = (now) => {
      const t = Math.min(1, (now - start) / dur);
      const e = 1 - Math.pow(1 - t, 3);
      el.textContent = nf.format(round2(from + (to - from) * e));
      if (t < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }

  // ---------------------------------------------------------------
  // Кешкићи картице (без емотикона)
  // ---------------------------------------------------------------
  function renderKeshkici() {
    const grid = $('#keshkici-grid');
    const list = state.data.summary.keshkici || [];

    if (!list.length) {
      grid.innerHTML = `
        <div class="empty" style="grid-column: 1 / -1; padding: 2rem 1rem;">
          <span class="empty-icon"><i data-lucide="wallet"></i></span>
          <p class="muted">Још ниси додао Кешкић за ${monthLabel(state.month)}.</p>
          <button class="btn btn-primary btn-sm" type="button" id="btn-seed-keshkic">
            <i data-lucide="plus"></i><span>Додај почетни Кешкић (Мама: ${money(state.defaultKeshkicVal)})</span>
          </button>
        </div>`;
      return;
    }

    grid.innerHTML = list.map((k) => {
      const isDef = k.isDefault && !k.dropped;
      const isActive = state.selectedKeshkicFilter === k.id;
      const isNeg = k.balance < 0;
      const pct = k.amountRsd > 0 ? (k.spentRsd / k.amountRsd) * 100 : (k.spentRsd > 0 ? 100 : 0);
      const initialInfo = k.currency !== 'RSD'
        ? `Почетно: ${money(k.amount, k.currency)} (${money(k.amountRsd)})`
        : `Почетно: ${money(k.amountRsd)}`;
      const sinceInfo = k.month !== state.month ? `Од: ${monthLabel(k.month)}` : '';
      const badge = k.dropped
        ? '<span class="k-badge-def is-dropped"><i data-lucide="archive"></i><span>Одбачен</span></span>'
        : (isDef ? '<span class="k-badge-def"><i data-lucide="star"></i><span>Подразумевани</span></span>' : '');

      return `
        <div class="k-card ${isActive ? 'is-active' : ''} ${isNeg ? 'is-negative' : ''} ${k.dropped ? 'is-dropped' : ''}" data-k-id="${k.id}">
          <div class="k-head">
            <div class="k-head-left">
              <div class="k-title" title="${esc(k.title)}">${esc(k.title)}</div>
              <div class="k-initial">${initialInfo}${sinceInfo ? ' · ' + sinceInfo : ''}</div>
            </div>
            ${badge}
          </div>

          <div class="k-balance-row">
            <div>
              <div class="k-balance-label">Преостало</div>
              <div class="k-balance">${money(k.balance)}</div>
            </div>
          </div>

          <div class="k-progress" title="Потрошено ${nf0.format(pct)}%">
            <div class="k-progress-bar ${pct >= 100 ? 'is-danger' : (pct >= 80 ? 'is-warn' : '')}" style="width: ${Math.min(100, pct)}%"></div>
          </div>

          <div class="k-foot">
            <span>Потрошено: ${money(k.spentRsd)} (${k.txCount || 0})</span>
            <div class="k-actions">
              ${(!isDef && !k.dropped) ? `<button class="btn btn-ghost btn-icon btn-xs" type="button" data-k-action="set-default" title="Постави као подразумевани"><i data-lucide="star"></i></button>` : ''}
              <button class="btn btn-ghost btn-icon btn-xs" type="button" data-k-action="edit" title="Измени"><i data-lucide="pencil"></i></button>
              ${k.dropped
                ? `<button class="btn btn-ghost btn-icon btn-xs" type="button" data-k-action="restore" title="Врати у употребу"><i data-lucide="archive-restore"></i></button>`
                : `<button class="btn btn-ghost btn-icon btn-xs" type="button" data-k-action="drop" title="Одбаци (више га не користим)"><i data-lucide="archive"></i></button>`}
              <button class="btn btn-ghost btn-icon btn-xs" type="button" data-k-action="delete" title="Обриши заувек"><i data-lucide="trash-2"></i></button>
            </div>
          </div>
        </div>`;
    }).join('');
  }

  // ---------------------------------------------------------------
  // Трошкови листа (без емотикона)
  // ---------------------------------------------------------------
  function renderTransactions() {
    const list = $('#tx-list');
    const txs = state.data.transactions || [];

    // Ажурирај филтер индикатор
    const filterInd = $('#filter-indicator');
    if (state.selectedKeshkicFilter > 0) {
      const activeK = (state.data.summary.keshkici || []).find((x) => x.id === state.selectedKeshkicFilter);
      $('#filter-name').textContent = activeK ? activeK.title : 'Кешкић';
      filterInd.hidden = false;
    } else {
      filterInd.hidden = true;
    }

    if (!txs.length) {
      const msg = state.selectedKeshkicFilter
        ? 'Нема забележених трошкова за овај Кешкић.'
        : 'Још нема унетих трошкова овог месеца.';
      list.innerHTML = `
        <div class="empty">
          <span class="empty-icon"><i data-lucide="receipt"></i></span>
          <p class="muted">${msg}</p>
          <button class="btn btn-primary btn-sm" type="button" data-action="new-tx">
            <i data-lucide="plus"></i><span>Додај трошак</span>
          </button>
        </div>`;
      return;
    }

    // Груписање по дану
    const byDay = new Map();
    for (const t of txs) {
      if (!byDay.has(t.date)) byDay.set(t.date, []);
      byDay.get(t.date).push(t);
    }

    let html = '';
    for (const [day, dayTxs] of byDay) {
      const d = parseDate(day);
      const isToday = day === today();
      const dayLabel = isToday ? 'Данас' : new Intl.DateTimeFormat(LOCALE, { weekday: 'short', day: 'numeric', month: 'short' }).format(d);
      const daySum = dayTxs.reduce((acc, t) => acc + t.amountRsd, 0);

      html += `<div class="tx-group">
        <div class="tx-day">
          <span><b>${cap(dayLabel)}</b> · ${day}</span>
          <span class="muted">${money(daySum)}</span>
        </div>`;

      for (const t of dayTxs) {
        const isFx = t.currency !== 'RSD';
        const fxPill = isFx ? `<span class="tx-fx">${money(t.amount, t.currency)} · 1 ${t.currency}=${nfRate.format(t.rate)}</span>` : '';
        const title = t.title ? esc(t.title) : '<span class="tx-title is-empty">Без описа</span>';
        const kBadge = `<span class="k-pill"><i data-lucide="wallet"></i>${esc(t.keshkicTitle || 'Кешкић')}</span>`;

        html += `
          <div class="tx" data-id="${t.id}">
            <div class="tx-icon is-expense"><i data-lucide="receipt"></i></div>
            <div class="tx-main">
              <div class="tx-title">${title}</div>
              <div class="tx-meta">${kBadge} ${fxPill}</div>
            </div>
            <div class="tx-amount">${money(t.amountRsd)}</div>
            <div class="tx-actions">
              <button class="btn btn-ghost btn-icon btn-sm" type="button" data-tx-action="edit" aria-label="Измени"><i data-lucide="pencil"></i></button>
              <button class="btn btn-ghost btn-icon btn-sm" type="button" data-tx-action="delete" aria-label="Обриши"><i data-lucide="trash-2"></i></button>
            </div>
          </div>`;
      }
      html += '</div>';
    }

    list.innerHTML = html;
  }

  // ---------------------------------------------------------------
  // Историја (6 месеци)
  // ---------------------------------------------------------------
  function renderHistory() {
    const el = $('#history');
    if (!state.history.length) {
      el.innerHTML = '<p class="muted small" style="padding:1rem;grid-column:1/-1;text-align:center;">Нема података</p>';
      return;
    }
    const maxVal = Math.max(...state.history.map((h) => Math.max(h.totalKeshkic, h.expenses)), 1);

    el.innerHTML = state.history.map((h) => {
      const availPct = Math.round((h.totalKeshkic / maxVal) * 100);
      const spentPct = Math.round((h.expenses / maxVal) * 100);
      const isActive = h.month === state.month;
      const isOver = h.expenses > h.totalKeshkic && h.totalKeshkic > 0;
      const label = monthName(h.month, 'short').slice(0, 3);

      return `
        <button class="h-col ${isActive ? 'is-active' : ''}" type="button" data-month="${h.month}"
          title="${monthLabel(h.month)}: Кешкићи ${money(h.totalKeshkic)}, трошкови ${money(h.expenses)}">
          <div class="h-bars">
            <div class="h-avail" style="height:${availPct}%;"></div>
            <div class="h-spent ${isOver ? 'is-over' : ''}" style="height:${spentPct}%;"></div>
          </div>
          <span class="h-label">${label}</span>
        </button>`;
    }).join('');
  }

  // ---------------------------------------------------------------
  // Форматирање поља
  // ---------------------------------------------------------------
  function formatField(input, formatter = nf) {
    const v = parseAmount(input.value);
    if (v > 0) input.value = formatter.format(v);
  }

  // ---------------------------------------------------------------
  // ЈЕДИНСТВЕНИ ДИЈАЛОГ: НОВА ТРАНСАКЦИЈА (Трошак / Кешкић)
  // ---------------------------------------------------------------
  const txDialog = $('#tx-dialog');

  function openModal(kind = 'expense', editItem = null, preset = {}) {
    // Нема ниједног Кешкића → прво мора Кешкић, а таб се усклађује са формом
    const hasK = state.data && state.data.summary && (state.data.summary.keshkici || []).some((k) => !k.dropped);
    if (kind === 'expense' && !editItem && !hasK) {
      kind = 'keshkic';
      preset = { title: 'Мама', amount: state.defaultKeshkicVal, isDefault: true, ...preset };
    }
    state.editingTx = (kind === 'expense' ? editItem : null);
    state.editingKeshkic = (kind === 'keshkic' ? editItem : null);
    $('#tx-error').hidden = true;

    // У току измене сакриј преклопник (segmented)
    const seg = $('#modal-segmented');
    if (editItem) {
      seg.style.display = 'none';
    } else {
      seg.style.display = 'grid';
    }

    const r = $(`input[name="modal-kind"][value="${kind}"]`);
    if (r) r.checked = true;

    updateModalFields(kind, editItem, preset);
    txDialog.showModal();
    icons();

    setTimeout(() => {
      if (kind === 'expense') {
        const amt = $('#tx-amount');
        amt.focus();
        if (editItem) amt.select();
      } else {
        const tit = $('#keshkic-title');
        tit.focus();
        if (editItem || preset.title) tit.select();
      }
    }, 60);
  }

  function updateModalFields(kind, editItem = null, preset = {}) {
    const isExpense = kind === 'expense';
    const s = state.data && state.data.summary ? state.data.summary : {};
    const keshkici = (s.keshkici || []).filter((k) => !k.dropped || (editItem && editItem.keshkicId === k.id));


    $('#modal-title').textContent = editItem
      ? (isExpense ? 'Измени трошак' : 'Измени Кешкић')
      : (isExpense ? 'Нови трошак' : 'Нови Кешкић');

    // Видљивост поља
    $('#field-keshkic-select').hidden = !isExpense;
    $('#field-tx-title').hidden = !isExpense;
    $('#field-tx-date').hidden = !isExpense;
    $('#field-keshkic-title').hidden = isExpense;
    $('#field-keshkic-default').hidden = isExpense;

    $('#label-amount').textContent = isExpense ? 'Износ трошка' : 'Почетни износ';
    $('#tx-submit-label').textContent = editItem ? 'Сачувај измене' : (isExpense ? 'Сачувај трошак' : 'Сачувај Кешкић');

    if (isExpense) {
      // Попуни падајући мени Кешкића (БЕЗ ЕМОТИКОНА)
      const select = $('#tx-keshkic');
      select.innerHTML = keshkici.length ? keshkici.map((k) => `
        <option value="${k.id}">
          ${esc(k.title)} (преостало: ${money(k.balance)})${k.isDefault ? ' [подразумевани]' : ''}
        </option>
      `).join('') : '<option value="">Нема Кешкића — прво додај Кешкић</option>';

      const defaultID = editItem
        ? editItem.keshkicId
        : (state.selectedKeshkicFilter > 0 ? state.selectedKeshkicFilter : s.defaultKeshkicId);
      if (defaultID) select.value = String(defaultID);

      $('#tx-amount').value = editItem ? nf.format(editItem.amount) : '';
      $('#tx-title').value = editItem ? editItem.title : '';
      $('#tx-date').value = editItem ? editItem.date : (state.month === currentMonth() ? today() : `${state.month}-01`);

      const cur = editItem ? editItem.currency : state.lastCurrency;
      $(`#cur-${cur}`).checked = true;
      setCurrency(cur, editItem && editItem.rate);
    } else {
      // Попуни поља за Кешкић
      $('#keshkic-title').value = editItem ? editItem.title : (preset.title || '');
      const amtVal = editItem ? editItem.amount : (preset.amount || '');
      $('#tx-amount').value = amtVal ? nf.format(amtVal) : '';

      const cur = editItem ? editItem.currency : (preset.currency || state.lastCurrency);
      $(`#cur-${cur}`).checked = true;
      setCurrency(cur, editItem && editItem.rate);

      $('#keshkic-is-default').checked = editItem
        ? editItem.isDefault
        : (keshkici.length === 0 || preset.isDefault || false);
    }
  }

  function setCurrency(cur, rate) {
    const isFx = cur !== 'RSD';
    $('#fx-row').hidden = !isFx;
    if (isFx) {
      $('#fx-cur').textContent = cur;
      const r = rate || (state.rates && state.rates.rates[cur]);
      $('#tx-rate').value = r ? nfRate.format(r) : '';
    }
    updateFx();
  }

  function updateFx() {
    const cur = $('input[name="currency"]:checked').value;
    if (cur === 'RSD') return;
    const amount = parseAmount($('#tx-amount').value);
    const rate = parseAmount($('#tx-rate').value);
    $('#fx-result').textContent = amount > 0 && rate > 0 ? money(amount * rate) : '— RSD';
  }

  async function submitModal(e) {
    e.preventDefault();
    const err = $('#tx-error');
    err.hidden = true;

    const kind = $('input[name="modal-kind"]:checked').value;
    const amount = parseAmount($('#tx-amount').value);
    const currency = $('input[name="currency"]:checked').value;
    const rate = currency === 'RSD' ? 1 : parseAmount($('#tx-rate').value);

    const fail = (msg) => { err.textContent = msg; err.hidden = false; };
    if (!(amount > 0)) return fail('Унеси износ већи од нуле, нпр. 500,00');
    if (!(rate > 0)) return fail('Унеси исправан курс, нпр. 117,20');

    const btn = $('#tx-submit');
    btn.disabled = true;

    try {
      if (kind === 'keshkic') {
        // ЧУВАЊЕ КЕШКИЋА
        const title = $('#keshkic-title').value.trim();
        if (!title) return fail('Унеси назив Кешкића.');
        const isDefault = $('#keshkic-is-default').checked;

        const body = { month: state.month, title, amount, currency, rate, isDefault };
        if (state.editingKeshkic) {
          await api(`/api/keshkici/${state.editingKeshkic.id}`, { method: 'PUT', body });
        } else {
          await api('/api/keshkici', { method: 'POST', body });
        }
        state.lastCurrency = currency;
        localStorage.setItem('keshk-currency', currency);
        txDialog.close();
        toast(state.editingKeshkic ? 'Кешкић измењен' : 'Кешкић сачуван');
        await load();
      } else {
        // ЧУВАЊЕ ТРОШКА
        const keshkicId = Number($('#tx-keshkic').value);
        if (!keshkicId) return fail('Изабери Кешкић из ког се троши.');
        const title = $('#tx-title').value.trim();
        const date = $('#tx-date').value;
        if (!date) return fail('Изабери датум.');

        const body = { keshkicId, amount, currency, rate, title, date };
        if (state.editingTx) {
          await api(`/api/transactions/${state.editingTx.id}`, { method: 'PUT', body });
        } else {
          await api('/api/transactions', { method: 'POST', body });
        }
        state.lastCurrency = currency;
        localStorage.setItem('keshk-currency', currency);
        txDialog.close();
        toast(state.editingTx ? 'Трошак измењен' : 'Трошак сачуван');
        state.month = date.slice(0, 7);
        await load();
      }
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) fail(ex.message);
    } finally {
      btn.disabled = false;
    }
  }

  async function setDefaultKeshkic(id) {
    try {
      await api(`/api/keshkici/${id}/default`, { method: 'PUT' });
      toast('Постављен као подразумевани Кешкић');
      await load();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function dropKeshkic(id, month) {
    try {
      await api(`/api/keshkici/${id}/drop`, { method: 'PUT', body: { month } });
      if (month && state.selectedKeshkicFilter === id) state.selectedKeshkicFilter = 0;
      toast(month ? 'Кешкић је одбачен од овог месеца (подаци су сачувани)' : 'Кешкић је враћен у употребу');
      await Promise.all([load(), loadHistory()]);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function deleteKeshkic(id, btn) {
    if (!btn.classList.contains('is-confirm')) {
      btn.classList.add('is-confirm');
      btn.textContent = 'Обриши?';
      setTimeout(() => {
        if (!btn.isConnected) return;
        btn.classList.remove('is-confirm');
        btn.innerHTML = '<i data-lucide="trash-2"></i>';
        icons();
      }, 3000);
      return;
    }
    try {
      await api(`/api/keshkici/${id}`, { method: 'DELETE' });
      if (state.selectedKeshkicFilter === id) state.selectedKeshkicFilter = 0;
      toast('Кешкић и његови трошкови обрисани');
      await load();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function deleteTx(id, btn) {
    if (!btn.classList.contains('is-confirm')) {
      btn.classList.add('is-confirm');
      btn.textContent = 'Обриши?';
      setTimeout(() => {
        if (!btn.isConnected) return;
        btn.classList.remove('is-confirm');
        btn.innerHTML = '<i data-lucide="trash-2"></i>';
        icons();
      }, 3000);
      return;
    }
    try {
      await api(`/api/transactions/${id}`, { method: 'DELETE' });
      toast('Трошак обрисан');
      await load();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  // ---------------------------------------------------------------
  // Тема
  // ---------------------------------------------------------------
  function toggleTheme() {
    const dark = !document.documentElement.classList.contains('dark');
    document.documentElement.classList.toggle('dark', dark);
    localStorage.setItem('keshk-theme', dark ? 'dark' : 'light');
    $('meta[name="theme-color"]').setAttribute('content', dark ? '#09090b' : '#ffffff');
  }

  // ---------------------------------------------------------------
  // Догађаји
  // ---------------------------------------------------------------
  function bind() {
    // Пријава
    $('#login-form').addEventListener('submit', async (e) => {
      e.preventDefault();
      const err = $('#login-error');
      const btn = $('#login-submit');
      err.hidden = true;
      btn.disabled = true;
      try {
        await api('/api/login', { method: 'POST', body: { username: $('#login-username').value, password: $('#login-password').value } });
        $('#login-password').value = '';
        await showApp(await api('/api/me'));
      } catch (ex) {
        err.textContent = ex.message;
        err.hidden = false;
      } finally {
        btn.disabled = false;
      }
    });

    $('#logout-btn').addEventListener('click', async () => {
      await api('/api/logout', { method: 'POST' }).catch(() => {});
      showLogin();
    });

    $('#theme-toggle').addEventListener('click', toggleTheme);
    $('#month-prev').addEventListener('click', () => goMonth(shiftMonth(state.month, -1)));
    $('#month-next').addEventListener('click', () => goMonth(shiftMonth(state.month, 1)));
    $('#month-label').addEventListener('click', () => goMonth(currentMonth()));

    // Главно дугме у горњој траци: "Нова" [N]
    $('#btn-new').addEventListener('click', () => openModal('expense'));
    $('#fab').addEventListener('click', () => openModal('expense'));

    // Брзо додавање са картица
    $('#add-keshkic-top').addEventListener('click', () => openModal('keshkic'));
    $('#add-tx-card').addEventListener('click', () => openModal('expense'));

    // Преклопник у дијалогу (Трошак / Кешкић)
    $$('input[name="modal-kind"]').forEach((r) => {
      r.addEventListener('change', () => {
        updateModalFields(r.value, state.editingTx || state.editingKeshkic);
        icons();
      });
    });

    // Форма у дијалогу
    $('#tx-form').addEventListener('submit', submitModal);
    $('#tx-amount').addEventListener('input', updateFx);
    $('#tx-rate').addEventListener('input', updateFx);
    $('#tx-amount').addEventListener('blur', (e) => formatField(e.target));
    $('#tx-rate').addEventListener('blur', (e) => formatField(e.target, nfRate));
    $$('input[name="currency"]').forEach((r) => r.addEventListener('change', () => setCurrency(r.value)));

    // Филтер индикатор
    $('#filter-clear').addEventListener('click', () => {
      state.selectedKeshkicFilter = 0;
      load();
    });

    // Кликови на картице Кешкића
    $('#keshkici-grid').addEventListener('click', (e) => {
      if (e.target.closest('#btn-seed-keshkic')) {
        return openModal('keshkic', null, { title: 'Мама', amount: state.defaultKeshkicVal, isDefault: true });
      }
      const actionBtn = e.target.closest('[data-k-action]');
      const card = e.target.closest('.k-card');
      if (!card) return;
      const id = Number(card.dataset.kId);
      const k = (state.data.summary.keshkici || []).find((x) => x.id === id);

      if (actionBtn) {
        const act = actionBtn.dataset.kAction;
        if (act === 'set-default') return setDefaultKeshkic(id);
        if (act === 'edit') return openModal('keshkic', k);
        if (act === 'drop') return dropKeshkic(id, state.month);
        if (act === 'restore') return dropKeshkic(id, '');
        if (act === 'delete') return deleteKeshkic(id, actionBtn);
        return;
      }

      // Клик на картицу филтрира трошкове
      if (state.selectedKeshkicFilter === id) state.selectedKeshkicFilter = 0;
      else state.selectedKeshkicFilter = id;
      load();
    });

    // Кликови на трансакције
    $('#tx-list').addEventListener('click', (e) => {
      if (e.target.closest('[data-action="new-tx"]')) return openModal('expense');
      const actionBtn = e.target.closest('[data-tx-action]');
      const row = e.target.closest('.tx');
      if (!row) return;
      const id = Number(row.dataset.id);
      const t = (state.data.transactions || []).find((x) => x.id === id);

      if (actionBtn) {
        const act = actionBtn.dataset.txAction;
        if (act === 'edit') return openModal('expense', t);
        if (act === 'delete') return deleteTx(id, actionBtn);
        return;
      }

      $$('.tx.is-open').forEach((r) => r !== row && r.classList.remove('is-open'));
      row.classList.toggle('is-open');
    });

    $('#tx-list').addEventListener('dblclick', (e) => {
      const row = e.target.closest('.tx');
      if (row && !e.target.closest('[data-tx-action]')) {
        const id = Number(row.dataset.id);
        openModal('expense', (state.data.transactions || []).find((x) => x.id === id));
      }
    });

    // Историја
    $('#history').addEventListener('click', (e) => {
      const col = e.target.closest('[data-month]');
      if (col) goMonth(col.dataset.month);
    });

    // Затварање дијалога
    $$('dialog').forEach((d) => {
      d.addEventListener('click', (e) => {
        if (e.target === d || e.target.closest('[data-close]')) d.close();
      });
    });

    // Пречице на тастатури
    document.addEventListener('keydown', (e) => {
      if ($('#app-view').hidden || document.querySelector('dialog[open]')) return;
      if (e.target.closest('input, textarea, select, [contenteditable]') || e.metaKey || e.ctrlKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (k === 'n' || k === 'н') { e.preventDefault(); openModal('expense'); }
      else if (k === 'k' || k === 'к') { e.preventDefault(); openModal('keshkic'); }
      else if (e.key === 'ArrowLeft') goMonth(shiftMonth(state.month, -1));
      else if (e.key === 'ArrowRight') goMonth(shiftMonth(state.month, 1));
      else if (k === 't' || k === 'т') goMonth(currentMonth());
    });

    document.addEventListener('visibilitychange', () => {
      if (!document.hidden && state.data && !$('#app-view').hidden) load();
    });
  }

  // ---------------------------------------------------------------
  // Покретање
  // ---------------------------------------------------------------
  async function boot() {
    icons();
    bind();
    try {
      await showApp(await api('/api/me'));
    } catch (e) {
      if (!(e instanceof Unauthorized)) showLogin();
    }
  }

  boot();
})();
