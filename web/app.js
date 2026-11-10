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
    balanceKeshkicId: 0,
    editingTx: null,
    editingKeshkic: null,
    editingDeposit: null,
    lastCurrency: localStorage.getItem('keshk-currency') || 'RSD',
    // Account & Admin
    account: null,
    currentProfile: null,
    authNoticeShown: false,
    admin: null,
    adminQuery: '',
    adminUsersPage: 1,
    adminInvitesPage: 1,
    onboarding: {
      code: '',
      username: '',
      displayName: '',
      password: '',
    },
    // Shopping lists
    lists: [],
    currentList: null,
    editingItemId: null,
    editingList: null,
  };

  function resetClientState() {
    state.month = currentMonth();
    state.data = null;
    state.history = [];
    state.selectedKeshkicFilter = 0;
    state.balanceKeshkicId = 0;
    state.editingTx = null;
    state.editingKeshkic = null;
    state.editingDeposit = null;
    state.account = null;
    state.currentProfile = null;
    state.admin = null;
    state.adminQuery = '';
    state.adminUsersPage = 1;
    state.adminInvitesPage = 1;
    state.lists = [];
    state.currentList = null;
    state.editingItemId = null;
    state.editingList = null;
    if ($('#keshkici-grid')) $('#keshkici-grid').innerHTML = '';
    if ($('#tx-list')) $('#tx-list').innerHTML = '';
    if ($('#lists-grid')) $('#lists-grid').innerHTML = '';
    if ($('#profile-container')) $('#profile-container').innerHTML = '';
    if ($('#settings-deletion-body')) $('#settings-deletion-body').innerHTML = '';
    if ($('#admin-deletions-list')) $('#admin-deletions-list').innerHTML = '';
    if ($('#admin-deletions-card')) $('#admin-deletions-card').hidden = true;
    if ($('#admin-invites-list')) $('#admin-invites-list').innerHTML = '';
    if ($('#admin-users-list')) $('#admin-users-list').innerHTML = '';
    if ($('#admin-invites-pager')) $('#admin-invites-pager').hidden = true;
    if ($('#admin-users-pager')) $('#admin-users-pager').hidden = true;
    if ($('#admin-user-search')) $('#admin-user-search').value = '';
    if ($('#admin-user-search-clear')) $('#admin-user-search-clear').hidden = true;
    if ($('#clear-opt-lists')) $('#clear-opt-lists').checked = true;
    if ($('#clear-opt-txs')) { $('#clear-opt-txs').checked = false; $('#clear-opt-txs').disabled = false; }
    if ($('#clear-opt-keshkici')) $('#clear-opt-keshkici').checked = false;
    $('#clear-opt-txs-card')?.classList.remove('is-locked');
  }

  // ---------------------------------------------------------------
  // API
  // ---------------------------------------------------------------
  class Unauthorized extends Error {}

  function isPublicEndpoint(path) {
    return path === '/api/login' || path.startsWith('/api/register');
  }

  async function api(path, { method = 'GET', body } = {}) {
    const res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    if (res.status === 401 && !isPublicEndpoint(path)) { showLogin(); throw new Unauthorized(); }
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
  // Приказ (Views) и Регистрација / Onboarding
  // ---------------------------------------------------------------
  function formatInviteCodeInput(raw) {
    const clean = String(raw || '').toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 12);
    if (clean.length > 8) return `${clean.slice(0, 4)}-${clean.slice(4, 8)}-${clean.slice(8)}`;
    if (clean.length > 4) return `${clean.slice(0, 4)}-${clean.slice(4)}`;
    return clean;
  }

  function resetOnboardingForms() {
    state.onboarding = { code: '', username: '', displayName: '', password: '' };
    if ($('#reg-code')) $('#reg-code').value = '';
    if ($('#reg-username')) $('#reg-username').value = '';
    if ($('#reg-display-name')) $('#reg-display-name').value = '';
    if ($('#reg-password')) $('#reg-password').value = '';
    if ($('#reg-password-confirm')) $('#reg-password-confirm').value = '';
    if ($('#reg-avatar')) $('#reg-avatar').value = '';
    if ($('#reg-banner')) $('#reg-banner').value = '';
    if ($('#reg-bio')) $('#reg-bio').value = '';
    if ($('#reg-code-error')) $('#reg-code-error').hidden = true;
    if ($('#reg-account-error')) $('#reg-account-error').hidden = true;
    if ($('#reg-profile-error')) $('#reg-profile-error').hidden = true;
  }

  function showOnboardingStep(step) {
    const badge = $('#ob-step-badge');
    const prog = $('#ob-progress');
    const b1 = $('#ob-bar-1');
    const b2 = $('#ob-bar-2');
    const fCode = $('#reg-code-form');
    const fAcc = $('#reg-account-form');
    const fProf = $('#reg-profile-form');

    if (fCode) fCode.hidden = step !== 'code';
    if (fAcc) fAcc.hidden = step !== 'account';
    if (fProf) fProf.hidden = step !== 'profile';

    if (step === 'code') {
      if (badge) badge.textContent = 'Позивница';
      if (prog) prog.hidden = true;
      if ($('#reg-code-error')) $('#reg-code-error').hidden = true;
      setTimeout(() => $('#reg-code')?.focus(), 40);
    } else if (step === 'account') {
      if (badge) badge.textContent = 'Корак 1 од 2';
      if (prog) prog.hidden = false;
      b1?.classList.add('is-active');
      b2?.classList.remove('is-active');
      if ($('#reg-account-error')) $('#reg-account-error').hidden = true;
      setTimeout(() => $('#reg-username')?.focus(), 40);
    } else if (step === 'profile') {
      if (badge) badge.textContent = 'Корак 2 од 2';
      if (prog) prog.hidden = false;
      b1?.classList.add('is-active');
      b2?.classList.add('is-active');
      if ($('#reg-profile-error')) $('#reg-profile-error').hidden = true;
      updateOnboardingPreview();
      setTimeout(() => $('#reg-avatar')?.focus(), 40);
    }
    icons();
  }

  function openRegisterCard(prefillCode = '') {
    $('#login-form').hidden = true;
    $('#register-card').hidden = false;
    if (prefillCode && $('#reg-code')) {
      $('#reg-code').value = formatInviteCodeInput(prefillCode);
    }
    showOnboardingStep('code');
  }

  function closeRegisterCard() {
    $('#register-card').hidden = true;
    $('#login-form').hidden = false;
    if ($('#login-error')) $('#login-error').hidden = true;
    $('#login-username')?.focus();
    icons();
  }

  function updateOnboardingPreview() {
    const uname = state.onboarding.username || 'korisnik';
    const dname = state.onboarding.displayName || uname;
    if ($('#reg-preview-name')) $('#reg-preview-name').textContent = dname;
    if ($('#reg-preview-handle')) $('#reg-preview-handle').textContent = `@${uname}`;
    setBannerBox($('#reg-preview-banner'), $('#reg-banner')?.value || '');
    setAvatarBox($('#reg-preview-avatar'), $('#reg-avatar')?.value || '', dname);
  }

  function showLogin({ manualLogout = false } = {}) {
    $('#splash').classList.add('is-hidden');
    $('#login-view').hidden = false;
    $('#app-view').hidden = true;

    const notice = $('#login-notice');
    const noticeText = $('#login-notice-text');
    const p = window.location.pathname;
    if (!manualLogout && (p === '/u' || p.startsWith('/u/'))) {
      let handle = '';
      try { handle = decodeURIComponent(p.slice(3)).replace(/\/+$/, '').replace(/^@+/, '').trim(); } catch {}
      const msg = handle
        ? `Мораш бити пријављен да би видео налог @${handle}.`
        : 'Мораш бити пријављен да би видео овај налог.';
      if (noticeText) noticeText.textContent = msg;
      if (notice) notice.hidden = false;
      if (!state.authNoticeShown) {
        state.authNoticeShown = true;
        toast(msg, 'error');
      }
    } else if (!manualLogout && (p === '/liste' || p.startsWith('/liste/') || p === '/podesavanja' || p.startsWith('/podesavanja/') || p === '/admin' || p.startsWith('/admin/'))) {
      if (noticeText) noticeText.textContent = 'Мораш бити пријављен да би приступио овој страници.';
      if (notice) notice.hidden = false;
    } else {
      if (notice) notice.hidden = true;
      state.authNoticeShown = false;
    }

    const urlCode = new URLSearchParams(window.location.search).get('code');
    if (urlCode && !manualLogout) {
      openRegisterCard(urlCode);
    } else {
      $('#register-card').hidden = true;
      $('#login-form').hidden = false;
      $('#login-username').focus();
    }
    icons();
  }

  async function showApp(me) {
    if (me) {
      if (me.defaultKeshkic) state.defaultKeshkicVal = me.defaultKeshkic;
      state.account = me;
      state.balanceKeshkicId = Number(localStorage.getItem(`keshk-balance-keshkic-${me.id}`)) || 0;
      updateTopbarAvatar();
    }
    state.authNoticeShown = false;
    if ($('#login-notice')) $('#login-notice').hidden = true;
    const bg = Promise.all([loadRates(), loadHistory()]);
    const routing = routeTo(window.location.pathname);
    $('#splash').classList.add('is-hidden');
    $('#login-view').hidden = true;
    $('#app-view').hidden = false;
    await Promise.all([routing, bg]);
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
    renderHistory();
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
    const cm = currentMonth();

    $('#month-text').textContent = monthLabel(state.month);
    $('#month-label').classList.toggle('is-current', state.month === cm);

    renderBalance();
    renderKeshkici();
    renderTransactions();
    renderHistory();
    icons();
  }

  function renderBalance() {
    if (!state.data) return;
    const summary = state.data.summary;
    const keshkici = summary.keshkici || [];
    const selected = keshkici.find((k) => k.id === state.balanceKeshkicId);
    if (!selected) state.balanceKeshkicId = 0;

    const name = $('#balance-keshkic-name');
    name.textContent = selected ? selected.title : '';
    name.hidden = !selected;
    $('#balance-settings').disabled = keshkici.length === 0;
    $('#balance-keshkici').innerHTML = [{ id: 0, title: 'Сви Кешкићи' }, ...keshkici].map((k) =>
      `<button class="dropdown-item" type="button" data-balance-keshkic="${k.id}" aria-pressed="${k.id === state.balanceKeshkicId}"><i data-lucide="check" aria-hidden="true"></i><span>${esc(k.title)}${k.dropped ? ' (одбачен)' : ''}</span></button>`
    ).join('');

    const s = selected ? {
      balance: selected.balance,
      totalKeshkic: selected.amountRsd + (selected.depositsRsd || 0),
      deposits: selected.depositsRsd || 0,
      expenses: selected.spentRsd,
      keshkici: [selected],
    } : summary;

    animateNumber($('#balance-value'), s.balance);
    $('#hero').classList.toggle('is-negative', s.balance < 0);
    const hint = $('#balance-hint');
    hint.textContent = balanceHint(s, currentMonth());
    hint.hidden = !hint.textContent;

    const pct = s.totalKeshkic > 0 ? (s.expenses / s.totalKeshkic) * 100 : (s.expenses > 0 ? 100 : 0);
    const bar = $('#progress-bar');
    bar.style.width = `${Math.min(100, pct)}%`;
    bar.classList.toggle('is-warn', pct >= 80 && pct < 100);
    bar.classList.toggle('is-danger', pct >= 100);
    $('#progress').setAttribute('aria-valuenow', String(Math.round(Math.min(100, pct))));
    $('#progress-spent').textContent = `Потрошено ${nf0.format(pct)}% · ${money(s.expenses)}`;
    $('#progress-total').textContent = `Буџет ${money(s.totalKeshkic)}`;
    $('#progress-total').title = s.deposits > 0 ? `Уплате: +${money(s.deposits)}` : '';
  }

  // Просек потрошње: све потрошено од почетка најранијег Кешкића (1. у месецу)
  // до данас (текући месец) односно до последњег дана приказаног месеца.
  function spendingAverage(s, cm) {
    const starts = (s.keshkici || []).map((k) => k.month).sort();
    if (!starts.length || state.month > cm) return null;
    const start = parseDate(`${starts[0]}-01`);
    const now = new Date();
    const end = state.month === cm
      ? new Date(now.getFullYear(), now.getMonth(), now.getDate())
      : parseDate(`${state.month}-${pad(daysIn(state.month))}`);
    const days = Math.max(1, Math.round((end - start) / 86400000) + 1);
    return { avg: s.expenses / days, days, since: starts[0] };
  }

  function balanceHint(s, cm) {
    if (!s.keshkici.length) return 'Додај први Кешкић преко „Ново“.';
    const a = spendingAverage(s, cm);
    if (!a || !(a.avg > 0)) return '';
    const avgText = `Просек ${money(a.avg)}/дан`;
    if (state.month === cm && s.balance > 0) {
      const left = Math.floor(s.balance / a.avg);
      return `${avgText} · још ≈ ${left} ${plural(left, 'дан', 'дана', 'дана')}`;
    }
    return avgText;
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
      const depTotal = k.depositsRsd || 0;
      const budget = k.amountRsd + depTotal;
      const pct = budget > 0 ? (k.spentRsd / budget) * 100 : (k.spentRsd > 0 ? 100 : 0);
      const initialInfo = k.currency !== 'RSD'
        ? `Почетно: ${money(k.amount, k.currency)} (${money(k.amountRsd)})`
        : `Почетно: ${money(k.amountRsd)}`;
      const spentInfo = `Потрошено: ${money(k.spentRsd)} · ${troskoviWord(k.txCount || 0)}`;
      const details = [initialInfo, `Уплате: ${money(depTotal)}`, spentInfo, `Од: ${monthLabel(k.month)}`].join('\n');
      const badge = k.dropped
        ? '<span class="k-mark is-dropped" title="Одбачен"><i data-lucide="archive"></i></span>'
        : (isDef ? '<span class="k-mark" title="Подразумевани"><i data-lucide="star"></i></span>' : '');

      return `
        <div class="k-card ${isActive ? 'is-active' : ''} ${isNeg ? 'is-negative' : ''} ${k.dropped ? 'is-dropped' : ''}" data-k-id="${k.id}">
          <div class="k-head">
            <div class="k-head-left">
              <div class="k-title-row">${badge}<div class="k-title" title="${esc(k.title)}">${esc(k.title)}</div></div>
              <button class="k-summary" type="button" data-k-action="details" title="${esc(details)}" aria-label="Детаљи Кешкића ${esc(k.title)}">
                <span>Буџет ${money(budget)}</span><i data-lucide="info" aria-hidden="true"></i>
              </button>
            </div>
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
            <span title="${esc(spentInfo)}">${nf0.format(pct)}% потрошено</span>
            <div class="k-actions">
              ${!k.dropped ? `<button class="btn btn-ghost btn-icon btn-xs" type="button" data-k-action="deposit" title="Додај уплату на овај Кешкић"><i data-lucide="arrow-down-left"></i></button>` : ''}
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

  function showKeshkicDetails(k) {
    if (!k) return;
    $('#keshkic-details-title').textContent = k.title;
    const initial = k.currency === 'RSD'
      ? money(k.amountRsd)
      : `${money(k.amount, k.currency)} (${money(k.amountRsd)})`;
    const rows = [
      ['Почетно', initial],
      ['Уплате', `${money(k.depositsRsd || 0)} · ${k.depositCount || 0} ${plural(k.depositCount || 0, 'уплата', 'уплате', 'уплата')}`],
      ['Потрошено', `${money(k.spentRsd)} · ${troskoviWord(k.txCount || 0)}`],
      ['Од', monthLabel(k.month)],
    ];
    $('#keshkic-details-body').innerHTML = rows.map(([label, value]) =>
      `<div><dt>${esc(label)}</dt><dd>${esc(value)}</dd></div>`
    ).join('');
    $('#keshkic-details-dialog').showModal();
  }

  // ---------------------------------------------------------------
  // Трошкови листа (без емотикона)
  // ---------------------------------------------------------------
  function renderTransactions() {
    const list = $('#tx-list');
    const txs = state.data.transactions || [];
    const deposits = state.data.deposits || [];

    // Ажурирај филтер индикатор
    const filterInd = $('#filter-indicator');
    if (state.selectedKeshkicFilter > 0) {
      const activeK = (state.data.summary.keshkici || []).find((x) => x.id === state.selectedKeshkicFilter);
      $('#filter-name').textContent = activeK ? activeK.title : 'Кешкић';
      filterInd.hidden = false;
    } else {
      filterInd.hidden = true;
    }

    if (!txs.length && !deposits.length) {
      const msg = state.selectedKeshkicFilter
        ? 'Нема забележених трошкова ни уплата за овај Кешкић.'
        : 'Још нема унетих трошкова ни уплата овог месеца.';
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

    // Груписање по дану (трошкови и уплате заједно, новији унос први)
    const byDay = new Map();
    for (const t of txs) {
      if (!byDay.has(t.date)) byDay.set(t.date, []);
      byDay.get(t.date).push({ ...t, _kind: 'expense' });
    }
    for (const d of deposits) {
      if (!byDay.has(d.date)) byDay.set(d.date, []);
      byDay.get(d.date).push({ ...d, _kind: 'deposit' });
    }
    const days = [...byDay.keys()].sort().reverse();

    let html = '';
    for (const day of days) {
      const dayItems = byDay.get(day).sort((a, b) =>
        String(b.createdAt || '').localeCompare(String(a.createdAt || '')) || b.id - a.id);
      const d = parseDate(day);
      const isToday = day === today();
      const dayLabel = isToday ? 'Данас' : new Intl.DateTimeFormat(LOCALE, { weekday: 'short', day: 'numeric', month: 'short' }).format(d);
      const daySpent = dayItems.filter((t) => t._kind === 'expense').reduce((acc, t) => acc + t.amountRsd, 0);
      const dayDeposited = dayItems.filter((t) => t._kind === 'deposit').reduce((acc, t) => acc + t.amountRsd, 0);
      const daySums = [
        daySpent > 0 || dayDeposited === 0 ? `<span class="muted">${money(daySpent)}</span>` : '',
        dayDeposited > 0 ? `<span class="is-income">+${money(dayDeposited)}</span>` : '',
      ].filter(Boolean).join('');

      html += `<div class="tx-group">
        <div class="tx-day">
          <span><b>${cap(dayLabel)}</b> · ${day}</span>
          <span class="tx-day-sums">${daySums}</span>
        </div>`;

      for (const t of dayItems) {
        const isDeposit = t._kind === 'deposit';
        const isFx = t.currency !== 'RSD';
        const fxPill = isFx ? `<span class="tx-fx">${money(t.amount, t.currency)} · 1 ${t.currency}=${nfRate.format(t.rate)}</span>` : '';
        const title = t.title
          ? esc(t.title)
          : (isDeposit ? 'Уплата' : '<span class="tx-title is-empty">Без описа</span>');
        const kBadge = `<span class="k-pill"><i data-lucide="wallet"></i>${esc(t.keshkicTitle || 'Кешкић')}</span>`;

        html += `
          <div class="tx ${isDeposit ? 'is-deposit' : ''}" data-id="${t.id}" data-kind="${t._kind}">
            <div class="tx-icon ${isDeposit ? 'is-income' : 'is-expense'}"><i data-lucide="${isDeposit ? 'arrow-down-left' : 'receipt'}"></i></div>
            <div class="tx-main">
              <div class="tx-title">${title}</div>
              <div class="tx-meta">${kBadge} ${fxPill}</div>
            </div>
            <div class="tx-amount ${isDeposit ? 'is-income' : ''}">${isDeposit ? '+' : ''}${money(t.amountRsd)}</div>
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
  // ЈЕДИНСТВЕНИ ДИЈАЛОГ: НОВА ТРАНСАКЦИЈА (Трошак / Уплата / Кешкић)
  // ---------------------------------------------------------------
  const txDialog = $('#tx-dialog');

  function openModal(kind = 'expense', editItem = null, preset = {}) {
    closeSettingsMenu();
    // Нема ниједног Кешкића → прво мора Кешкић, а таб се усклађује са формом
    const hasK = state.data && state.data.summary && (state.data.summary.keshkici || []).some((k) => !k.dropped);
    $$('#kind-expense, #kind-deposit').forEach((r) => { r.disabled = !hasK && !editItem; });
    if ((kind === 'expense' || kind === 'deposit') && !editItem && !hasK) {
      kind = 'keshkic';
      preset = { title: 'Мама', amount: state.defaultKeshkicVal, isDefault: true, ...preset };
    }
    state.editingTx = (kind === 'expense' ? editItem : null);
    state.editingDeposit = (kind === 'deposit' ? editItem : null);
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
      if (kind === 'expense' || kind === 'deposit') {
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
    const isKeshkic = kind === 'keshkic';
    const isDeposit = kind === 'deposit';
    const isTxLike = !isKeshkic; // трошак или уплата деле иста поља
    const preserveTxFields = isTxLike && !editItem && txDialog.open && !$('#field-keshkic-select').hidden;
    const selectedKeshkic = preserveTxFields ? Number($('#tx-keshkic').value) : 0;
    const s = state.data && state.data.summary ? state.data.summary : {};
    const keshkici = (s.keshkici || []).filter((k) => !k.dropped || (editItem && editItem.keshkicId === k.id));

    if (editItem) {
      $('#modal-title').textContent = isDeposit ? 'Измени уплату' : (isKeshkic ? 'Измени Кешкић' : 'Измени трошак');
    } else {
      $('#modal-title').textContent = isDeposit ? 'Нова уплата' : (isKeshkic ? 'Нови Кешкић' : 'Нови трошак');
    }

    // Видљивост поља
    $('#field-keshkic-select').hidden = !isTxLike;
    $('#field-tx-title').hidden = !isTxLike;
    $('#field-tx-date').hidden = !isTxLike;
    $('#field-keshkic-title').hidden = isTxLike;
    $('#field-keshkic-default').hidden = isTxLike;

    $('#label-keshkic-select').textContent = isDeposit ? 'На који Кешкић се уплаћује?' : 'Из ког Кешкића се троши?';
    $('#label-tx-title').textContent = isDeposit ? 'Од кога / опис' : 'Опис';
    $('#tx-title').placeholder = isDeposit ? 'нпр. поклон, поклон, поклон…' : 'нпр. кафа, Steam, превоз…';

    $('#label-amount').textContent = isDeposit ? 'Износ уплате' : (isKeshkic ? 'Почетни износ' : 'Износ трошка');
    $('#tx-submit-label').textContent = editItem
      ? 'Сачувај измене'
      : (isDeposit ? 'Сачувај уплату' : (isKeshkic ? 'Сачувај Кешкић' : 'Сачувај трошак'));

    if (isTxLike) {
      // Попуни падајући мени Кешкића (БЕЗ ЕМОТИКОНА)
      const select = $('#tx-keshkic');
      select.innerHTML = keshkici.length ? keshkici.map((k) => `
        <option value="${k.id}">
          ${esc(k.title)} (преостало: ${money(k.balance)})${k.isDefault ? ' [подразумевани]' : ''}
        </option>
      `).join('') : '<option value="">Нема Кешкића — прво додај Кешкић</option>';

      const defaultID = editItem
        ? editItem.keshkicId
        : (selectedKeshkic || preset.keshkicId || (state.selectedKeshkicFilter > 0 ? state.selectedKeshkicFilter : s.defaultKeshkicId));
      if (keshkici.some((k) => k.id === defaultID)) select.value = String(defaultID);

      if (preserveTxFields) return;

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
      } else if (kind === 'deposit') {
        // ЧУВАЊЕ УПЛАТЕ (додатни новац на постојећи Кешкић)
        const keshkicId = Number($('#tx-keshkic').value);
        if (!keshkicId) return fail('Изабери Кешкић на који се уплаћује.');
        const title = $('#tx-title').value.trim();
        const date = $('#tx-date').value;
        if (!date) return fail('Изабери датум.');

        const body = { keshkicId, amount, currency, rate, title, date };
        if (state.editingDeposit) {
          await api(`/api/deposits/${state.editingDeposit.id}`, { method: 'PUT', body });
        } else {
          await api('/api/deposits', { method: 'POST', body });
        }
        state.lastCurrency = currency;
        localStorage.setItem('keshk-currency', currency);
        txDialog.close();
        toast(state.editingDeposit ? 'Уплата измењена' : 'Уплата сачувана');
        state.month = date.slice(0, 7);
        await Promise.all([load(), loadHistory()]);
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

  async function deleteDeposit(id, btn) {
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
      await api(`/api/deposits/${id}`, { method: 'DELETE' });
      toast('Уплата обрисана');
      await Promise.all([load(), loadHistory()]);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  // ---------------------------------------------------------------
  // Извоз / увоз података
  // ---------------------------------------------------------------
  async function exportData() {
    closeSettingsMenu();
    try {
      const res = await fetch('/api/export', { credentials: 'same-origin' });
      if (res.status === 401) { showLogin(); return; }
      if (!res.ok) throw new Error(`Грешка (${res.status})`);
      const blob = await res.blob();
      const uname = (state.account && state.account.username) ? state.account.username : 'backup';
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = `keshk-${uname}-${today()}.json`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 1000);
      toast('Подаци извезени');
    } catch (ex) {
      toast(ex.message, 'error');
    }
  }

  async function importData(file) {
    if (!file) return;
    if (!confirm('Увоз ЗАМЕЊУЈЕ све тренутне Кешкиће, трошкове, уплате и листе на твом налогу подацима из датотеке.\n\nНалог (лозинка, слика) остаје исти. Наставити?')) return;
    try {
      const text = await file.text();
      try { JSON.parse(text); } catch { throw new Error('Датотека није исправан JSON'); }
      const r = await api('/api/import', { method: 'POST', body: JSON.parse(text) });
      const depTxt = r.deposits ? `, ${r.deposits} ${plural(r.deposits, 'уплата', 'уплате', 'уплата')}` : '';
      toast(`Увезено: ${r.keshkici} Кешкића, ${troskoviWord(r.transactions)}${depTxt}, ${r.lists} листа`);
      state.data = null;
      state.currentList = null;
      state.selectedKeshkicFilter = 0;
      await loadHistory();
      await load();
      await routeTo(window.location.pathname);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  function toggleTheme() {
    const dark = !document.documentElement.classList.contains('dark');
    document.documentElement.classList.toggle('dark', dark);
    localStorage.setItem('keshk-theme', dark ? 'dark' : 'light');
    $('meta[name="theme-color"]').setAttribute('content', dark ? '#09090b' : '#ffffff');
    updateThemeLabel();
  }
  function updateThemeLabel() {
    const el = $('#menu-theme-label');
    if (el) el.textContent = document.documentElement.classList.contains('dark') ? 'Светла тема' : 'Тамна тема';
  }

  // ---------------------------------------------------------------
  // Дропдаун Подешавања
  // ---------------------------------------------------------------
  function closeSettingsMenu() {
    const menu = $('#settings-menu');
    if (menu && !menu.hidden) {
      menu.hidden = true;
      $('#settings-trigger')?.setAttribute('aria-expanded', 'false');
    }
  }

  function toggleSettingsMenu(e) {
    if (e) e.stopPropagation();
    const menu = $('#settings-menu');
    if (!menu) return;
    const open = menu.hidden;
    menu.hidden = !open;
    $('#settings-trigger')?.setAttribute('aria-expanded', String(open));
    if (open) {
      updateThemeLabel();
      if (menu.querySelector('[data-lucide]')) icons();
    }
  }

  // ---------------------------------------------------------------
  // Профили корисника (/u/@korisnik) и налог
  // ---------------------------------------------------------------
  const MONTHS_GEN = [
    'јануара', 'фебруара', 'марта', 'априла', 'маја', 'јуна',
    'јула', 'августа', 'септембра', 'октобра', 'новембра', 'децембра',
  ];

  function formatMemberSince(s) {
    if (!s || !/^\d{4}-\d{2}-\d{2}$/.test(s)) return 'недавно';
    const [y, m, d] = s.split('-').map(Number);
    const mName = MONTHS_GEN[m - 1];
    return mName ? `${d}. ${mName} ${y}.` : s;
  }

  const isHttpUrl = (u) => /^https?:\/\/\S+$/i.test(String(u || '').trim());

  function setAvatarBox(el, url, altText = 'Аватар') {
    if (!el) return;
    const clean = String(url || '').trim();
    const fallback = () => {
      if (el.dataset.avatarSrc === '__fallback__' && el.querySelector('svg, [data-lucide]')) return;
      el.dataset.avatarSrc = '__fallback__';
      el.innerHTML = '<i data-lucide="user"></i>';
      icons();
    };
    if (!isHttpUrl(clean)) {
      fallback();
      return;
    }
    if (el.dataset.avatarSrc === clean && el.querySelector('img')) {
      el.querySelector('img').alt = altText;
      return;
    }
    el.dataset.avatarSrc = clean;
    const img = document.createElement('img');
    img.alt = altText;
    img.draggable = false;
    img.addEventListener('error', fallback, { once: true });
    img.src = clean;
    el.replaceChildren(img);
  }

  function setBannerBox(el, url) {
    if (!el) return;
    const clean = String(url || '').trim();
    if (!isHttpUrl(clean)) {
      delete el.dataset.bannerSrc;
      el.innerHTML = '';
      return;
    }
    if (el.dataset.bannerSrc === clean && el.querySelector('img')) return;
    el.dataset.bannerSrc = clean;
    el.innerHTML = '';
    const img = document.createElement('img');
    img.alt = '';
    img.draggable = false;
    img.addEventListener('error', () => {
      delete el.dataset.bannerSrc;
      img.remove();
    }, { once: true });
    img.src = clean;
    el.appendChild(img);
  }

  function updateTopbarAvatar() {
    const a = state.account || {};
    setAvatarBox($('#topbar-avatar'), a.avatar, a.displayName || a.username || 'Аватар');
    const nameEl = $('#menu-user-name');
    if (nameEl) nameEl.textContent = a.displayName || a.username || 'admin';
    const handleEl = $('#menu-user-handle');
    if (handleEl) handleEl.textContent = '@' + (a.username || 'admin');
    const adminItem = $('#menu-admin');
    if (adminItem) adminItem.hidden = !a.isAdmin;
  }

  function openMyProfile() {
    closeSettingsMenu();
    const uname = (state.account && state.account.username) || 'admin';
    navigate(`/u/@${encodeURIComponent(uname)}`);
  }

  async function loadProfilePage(uname, seq) {
    const box = $('#profile-container');
    if (!box) return;
    if (!uname) {
      renderProfileNotFound('');
      return;
    }
    try {
      const p = await api(`/api/users/@${encodeURIComponent(uname)}`);
      if (seq && seq !== routeSeq) return;
      state.currentProfile = p;
      document.title = `Keshk >> @${p.username}`;
      renderProfilePage(p);
    } catch (ex) {
      if (ex instanceof Unauthorized || (seq && seq !== routeSeq)) return;
      state.currentProfile = null;
      document.title = 'Keshk >> Налог';
      renderProfileNotFound(uname);
    }
  }

  function renderProfileNotFound(uname) {
    const box = $('#profile-container');
    if (!box) return;
    box.innerHTML = `
      <div class="card lists-page-empty">
        <span class="lists-page-empty-icon"><i data-lucide="user-x"></i></span>
        <h3>Корисник није пронађен</h3>
        <p>${uname ? `Налог <b>@${esc(uname)}</b> не постоји.` : 'Тражени налог не постоји.'}</p>
        <button id="btn-notfound-home" class="btn btn-outline btn-sm" type="button">
          <i data-lucide="arrow-left"></i><span>Назад на контролну таблу</span>
        </button>
      </div>`;
    icons();
  }

  function renderProfilePage(p) {
    const box = $('#profile-container');
    if (!box || !p) return;
    const title = p.displayName || p.username;
    const bioHtml = p.bio
      ? `<p class="profile-bio">${esc(p.bio)}</p>`
      : `<p class="profile-bio is-empty">${p.isSelf ? '' : 'Корисник није додао опис.'}</p>`;
    const adminBadge = p.isAdmin
      ? `<span class="badge badge-admin"><i data-lucide="shield"></i><span>Админ</span></span>`
      : '';

    box.innerHTML = `
      <section class="card profile-card">
        <div class="profile-banner" id="profile-banner-box"></div>
        <div class="profile-body">
          <div class="profile-top">
            <div class="profile-avatar" id="profile-avatar-box">
              <i data-lucide="user"></i>
            </div>
            <div class="profile-actions">
              <button class="btn btn-outline btn-icon btn-sm" type="button" id="btn-copy-profile" title="Копирај линк налога" aria-label="Копирај линк налога">
                <i data-lucide="link-2"></i>
              </button>
              <button class="btn btn-outline btn-icon btn-sm" type="button" id="btn-qr-profile" title="Прикажи QR код налога" aria-label="Прикажи QR код налога">
                <i data-lucide="qr-code"></i>
              </button>
            </div>
          </div>

          <div class="profile-main">
            <div class="profile-names">
              <h1 class="profile-name">${esc(title)}</h1>
              <span class="profile-handle">@${esc(p.username)}</span>
            </div>
            ${bioHtml}
            <div class="profile-meta">
              ${adminBadge}
              <span class="badge badge-outline">
                <i data-lucide="calendar"></i>
                <span>Члан од ${esc(formatMemberSince(p.memberSince))}</span>
              </span>
            </div>
          </div>
        </div>
      </section>`;

    setBannerBox($('#profile-banner-box'), p.banner);
    setAvatarBox($('#profile-avatar-box'), p.avatar, title);
    icons();
  }

  function openProfileQRDialog(p) {
    if (!p) return;
    const origin = window.location.origin;
    const fullUrl = `${origin}/u/@${encodeURIComponent(p.username)}`;
    const qrSvgUrl = `/api/users/@${encodeURIComponent(p.username)}/qr?origin=${encodeURIComponent(origin)}`;
    const qrPngUrl = `${qrSvgUrl}&format=png`;

    const labelEl = $('#qr-user-label');
    if (labelEl) {
      labelEl.textContent = p.displayName ? `${p.displayName} · @${p.username}` : `@${p.username}`;
    }
    const urlEl = $('#qr-user-url');
    if (urlEl) {
      urlEl.textContent = fullUrl;
      urlEl.dataset.url = fullUrl;
    }
    const imgEl = $('#qr-image');
    if (imgEl) {
      imgEl.src = qrSvgUrl;
      imgEl.alt = `QR код за @${p.username}`;
    }
    const dlEl = $('#qr-download-png');
    if (dlEl) {
      dlEl.href = qrPngUrl;
      dlEl.download = `keshk-@${p.username}-qr.png`;
    }
    $('#qr-dialog')?.showModal();
    icons();
  }

  function openProfileDialog() {
    closeSettingsMenu();
    const a = state.account || {};
    $('#prof-display-name').value = a.displayName || '';
    $('#prof-bio').value = a.bio || '';
    $('#prof-avatar').value = a.avatar || '';
    $('#prof-banner').value = a.banner || '';
    $('#prof-error').hidden = true;
    updateProfilePreview();
    $('#profile-dialog').showModal();
    icons();
  }

  function updateProfilePreview() {
    setBannerBox($('#prof-preview-banner'), $('#prof-banner').value);
    setAvatarBox($('#prof-preview-avatar'), $('#prof-avatar').value);
  }

  async function saveProfile(e) {
    e.preventDefault();
    const err = $('#prof-error');
    err.hidden = true;
    const fail = (msg) => { err.textContent = msg; err.hidden = false; };

    const displayName = $('#prof-display-name').value.trim();
    const bio = $('#prof-bio').value.trim();
    const avatar = $('#prof-avatar').value.trim();
    const banner = $('#prof-banner').value.trim();

    if (avatar && !isHttpUrl(avatar)) return fail('Линк профилне слике мора почињати са https:// или http://');
    if (banner && !isHttpUrl(banner)) return fail('Линк позадине мора почињати са https:// или http://');

    const btn = $('#prof-save');
    btn.disabled = true;
    try {
      state.account = await api('/api/account', {
        method: 'PUT',
        body: { displayName, bio, avatar, banner },
      });
      updateTopbarAvatar();
      $('#profile-dialog').close();
      toast('Налог сачуван');
      if (state.currentProfile && state.currentProfile.isSelf) {
        await loadProfilePage(state.account.username);
      }
      if (window.location.pathname.startsWith('/podesavanja')) {
        renderSettingsPage();
      }
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) fail(ex.message);
    } finally {
      btn.disabled = false;
    }
  }

  function openPasswordDialog() {
    closeSettingsMenu();
    $('#pw-current').value = '';
    $('#pw-next').value = '';
    $('#pw-error').hidden = true;
    $('#password-dialog').showModal();
    icons();
    setTimeout(() => $('#pw-current').focus(), 60);
  }

  async function changePassword(e) {
    e.preventDefault();
    const err = $('#pw-error');
    err.hidden = true;
    const current = $('#pw-current').value;
    const next = $('#pw-next').value;
    if (!current || !next) { err.textContent = 'Попуни оба поља.'; err.hidden = false; return; }
    const btn = $('#pw-save');
    btn.disabled = true;
    try {
      await api('/api/account/password', { method: 'PUT', body: { current, next } });
      toast('Лозинка промењена');
      $('#pw-current').value = '';
      $('#pw-next').value = '';
      $('#password-dialog').close();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) { err.textContent = ex.message; err.hidden = false; }
    } finally {
      btn.disabled = false;
    }
  }

  // ---------------------------------------------------------------
  // Подешавања (/podesavanja) — брисање података и захтев за брисање налога
  // ---------------------------------------------------------------
  function buildDeletionPrompt(a, placeholder = '[генериши потврдни код]') {
    const id = a && a.id != null ? a.id : '';
    const uname = (a && a.username) || '';
    const code = (a && a.deletionCode) ? a.deletionCode : placeholder;
    return `Захтевам брисање налога:\nID: ${id}\nКорисничко име: ${uname}\nКОД: ${code}`;
  }

  function syncClearCheckboxes() {
    const kCb = $('#clear-opt-keshkici');
    const tCb = $('#clear-opt-txs');
    const tCard = $('#clear-opt-txs-card');
    if (!kCb || !tCb) return;
    if (kCb.checked) {
      tCb.checked = true;
      tCb.disabled = true;
      tCard?.classList.add('is-locked');
    } else {
      tCb.disabled = false;
      tCard?.classList.remove('is-locked');
    }
    if ($('#clear-data-error')) $('#clear-data-error').hidden = true;
  }

  async function loadSettingsPage(seq) {
    try {
      const me = await api('/api/me');
      if (seq && seq !== routeSeq) return;
      state.account = me;
      updateTopbarAvatar();
      renderSettingsPage();
    } catch (ex) {
      if (!(ex instanceof Unauthorized) && (!seq || seq === routeSeq)) {
        toast(ex.message, 'error');
      }
    }
  }

  function renderSettingsPage() {
    syncClearCheckboxes();
    const box = $('#settings-deletion-body');
    if (!box) return;
    const a = state.account || {};

    if (a.isAdmin) {
      box.innerHTML = `
        <div class="deletion-info-box">
          <i data-lucide="shield"></i>
          <div>
            Ово је главни администраторски налог (<b>ID: #${esc(a.id)}</b>, <b>@${esc(a.username)}</b>) и не може се обрисати.
            Захтеве других корисника за брисање налога можеш прегледати и обрадити у
            <button class="admin-user-link" type="button" data-go-admin>Админ панелу</button>.
          </div>
        </div>`;
      icons();
      return;
    }

    const hasCode = Boolean(a.deletionCode);
    const promptText = buildDeletionPrompt(a);
    const reqDay = a.deletionRequestedAt ? a.deletionRequestedAt.split('T')[0] : '';

    if (!hasCode) {
      box.innerHTML = `
        <div class="deletion-info-box">
          <i data-lucide="info"></i>
          <div>
            Да би се потврдило да брисање заиста тражи власник налога, прво генериши потврдни код и пошаљи поруку у наставку на <b>admin@crnbg.org</b>.
            Код можеш генерисати <b>само једном</b> док га администратор не потврди.
          </div>
        </div>

        <div class="deletion-prompt-wrap">
          <div class="deletion-prompt-head">
            <span>Спреман захтев за admin@crnbg.org</span>
            <span class="badge badge-outline"><i data-lucide="key-round"></i><span>Код није генерисан</span></span>
          </div>
          <pre class="deletion-prompt-pre">${esc(promptText)}</pre>
        </div>

        <div class="deletion-actions">
          <button id="btn-gen-deletion-code" class="btn btn-outline btn-sm is-danger-btn" type="button">
            <i data-lucide="key-round"></i><span>Генериши потврдни код</span>
          </button>
        </div>`;
      icons();
      return;
    }

    const mailSubject = encodeURIComponent(`Захтев за брисање налога (@${a.username})`);
    const mailBody = encodeURIComponent(promptText);
    const mailHref = `mailto:admin@crnbg.org?subject=${mailSubject}&body=${mailBody}`;

    box.innerHTML = `
      <div class="deletion-info-box is-warning">
        <i data-lucide="shield-alert"></i>
        <div>
          Потврдни код је генерисан${reqDay ? ` (${esc(reqDay)})` : ''}. Копирај захтев испод и пошаљи га на <b>admin@crnbg.org</b>.
          Нови код није могуће поново генерисати док га администратор не потврди.
        </div>
      </div>

      <div class="deletion-prompt-wrap">
        <div class="deletion-prompt-head">
          <span>Спреман захтев за admin@crnbg.org</span>
          <span class="badge badge-danger"><i data-lucide="lock"></i><span>Код генерисан (закључано)</span></span>
        </div>
        <pre class="deletion-prompt-pre" id="deletion-prompt-text">${esc(promptText)}</pre>
      </div>

      <div class="deletion-actions">
        <button class="btn btn-primary btn-sm" type="button" data-copy-deletion-prompt>
          <i data-lucide="copy"></i><span>Копирај захтев</span>
        </button>
        <a class="btn btn-outline btn-sm" href="${mailHref}">
          <i data-lucide="mail"></i><span>Пошаљи на admin@crnbg.org</span>
        </a>
        <button class="btn btn-outline btn-sm" type="button" disabled>
          <i data-lucide="lock"></i><span>Код се може генерисати само једном</span>
        </button>
      </div>`;
    icons();
  }

  async function submitClearData(btn) {
    const err = $('#clear-data-error');
    if (err) err.hidden = true;

    const lists = Boolean($('#clear-opt-lists')?.checked);
    const keshkici = Boolean($('#clear-opt-keshkici')?.checked);
    const transactions = keshkici || Boolean($('#clear-opt-txs')?.checked);

    if (!lists && !transactions && !keshkici) {
      if (err) {
        err.textContent = 'Изабери бар једну врсту података коју желиш да обришеш.';
        err.hidden = false;
      }
      return;
    }

    if (!btn.classList.contains('is-confirm-danger')) {
      btn.classList.add('is-confirm-danger');
      btn.innerHTML = '<i data-lucide="alert-triangle"></i><span>Потврди брисање података?</span>';
      icons();
      setTimeout(() => {
        if (!btn.isConnected || !btn.classList.contains('is-confirm-danger')) return;
        btn.classList.remove('is-confirm-danger');
        btn.innerHTML = '<i data-lucide="trash-2"></i><span>Обриши податке</span>';
        icons();
      }, 4000);
      return;
    }

    btn.disabled = true;
    try {
      await api('/api/account/clear-data', {
        method: 'POST',
        body: { keshkici, transactions, lists },
      });
      btn.classList.remove('is-confirm-danger');
      btn.innerHTML = '<i data-lucide="trash-2"></i><span>Обриши податке</span>';
      icons();
      state.data = null;
      state.lists = [];
      state.currentList = null;
      state.selectedKeshkicFilter = 0;
      toast('Изабрани подаци су обрисани');
      await Promise.all([load(), loadHistory()]);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) {
        if (err) {
          err.textContent = ex.message;
          err.hidden = false;
        } else {
          toast(ex.message, 'error');
        }
      }
    } finally {
      btn.disabled = false;
    }
  }

  async function generateDeletionCode(btn) {
    if (!btn.classList.contains('is-confirm-danger')) {
      btn.classList.add('is-confirm-danger');
      btn.innerHTML = '<i data-lucide="alert-triangle"></i><span>Потврди (могуће само једном)?</span>';
      icons();
      setTimeout(() => {
        if (!btn.isConnected || !btn.classList.contains('is-confirm-danger')) return;
        btn.classList.remove('is-confirm-danger');
        btn.innerHTML = '<i data-lucide="key-round"></i><span>Генериши потврдни код</span>';
        icons();
      }, 4000);
      return;
    }

    btn.disabled = true;
    try {
      const updated = await api('/api/account/deletion-code', { method: 'POST' });
      state.account = updated;
      renderSettingsPage();
      await copyText(buildDeletionPrompt(updated), 'Потврдни код је генерисан и захтев је копиран');
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) {
        toast(ex.message, 'error');
        await loadSettingsPage();
      }
    } finally {
      if (btn.isConnected) btn.disabled = false;
    }
  }

  // ---------------------------------------------------------------
  // Админ панел (/admin)
  // ---------------------------------------------------------------
  async function copyText(text, successMsg) {
    try {
      await navigator.clipboard.writeText(text);
      toast(successMsg);
    } catch {
      toast(text);
    }
  }

  let adminReqSeq = 0;
  let adminSearchTimer = null;

  async function loadAdminPage(seq) {
    const reqId = ++adminReqSeq;
    try {
      const params = new URLSearchParams();
      params.set('page', String(state.adminUsersPage || 1));
      params.set('invitesPage', String(state.adminInvitesPage || 1));
      if (state.adminQuery) params.set('q', state.adminQuery);

      const data = await api(`/api/admin/overview?${params.toString()}`);
      if (reqId !== adminReqSeq || (seq && seq !== routeSeq)) return;
      state.admin = data;
      if (data.usersMeta && data.usersMeta.page) state.adminUsersPage = data.usersMeta.page;
      if (data.invitesMeta && data.invitesMeta.page) state.adminInvitesPage = data.invitesMeta.page;
      renderAdminPage();
    } catch (ex) {
      if (ex instanceof Unauthorized || reqId !== adminReqSeq || (seq && seq !== routeSeq)) return;
      toast(ex.message, 'error');
      navigate('/');
    }
  }

  function renderAdminPager(pagerSel, infoSel, prevSel, nextSel, meta) {
    const pager = $(pagerSel);
    if (!pager || !meta) return;
    if (meta.totalPages <= 1) {
      pager.hidden = true;
      return;
    }
    const start = (meta.page - 1) * meta.pageSize + 1;
    const end = Math.min(meta.total, meta.page * meta.pageSize);
    const info = $(infoSel);
    if (info) {
      info.textContent = `Приказано ${start}–${end} од ${meta.total} · страна ${meta.page} од ${meta.totalPages}`;
    }
    const prevBtn = $(prevSel);
    const nextBtn = $(nextSel);
    if (prevBtn) prevBtn.disabled = meta.page <= 1;
    if (nextBtn) nextBtn.disabled = meta.page >= meta.totalPages;
    pager.hidden = false;
  }

  function renderAdminPage() {
    if (!state.admin) return;
    const users = state.admin.users || [];
    const invites = state.admin.invites || [];
    const st = state.admin.stats || {};
    const totalUsers = st.totalUsers != null ? st.totalUsers : users.length;
    const activeCount = st.activeInvites != null ? st.activeInvites : invites.filter((c) => !c.usedAt && !c.usedBy).length;
    const usedCount = st.usedInvites != null ? st.usedInvites : invites.length - activeCount;
    const members = Math.max(0, totalUsers - 1);

    if ($('#admin-stat-users')) $('#admin-stat-users').textContent = String(totalUsers);
    if ($('#admin-stat-users-sub')) {
      $('#admin-stat-users-sub').textContent = members > 0
        ? `1 админ · ${members} ${plural(members, 'члан', 'члана', 'чланова')}`
        : 'само администратор';
    }
    if ($('#admin-stat-active-codes')) $('#admin-stat-active-codes').textContent = String(activeCount);
    if ($('#admin-stat-active-sub')) {
      $('#admin-stat-active-sub').textContent = activeCount > 0
        ? `${activeCount} ${plural(activeCount, 'код спреман', 'кода спремна', 'кодова спремно')}`
        : 'нема активних кодова';
    }
    if ($('#admin-stat-used-codes')) $('#admin-stat-used-codes').textContent = String(usedCount);
    if ($('#admin-stat-used-sub')) {
      $('#admin-stat-used-sub').textContent = `${usedCount} ${plural(usedCount, 'регистрација', 'регистрације', 'регистрација')}`;
    }

    const pendingDeletions = state.admin.pendingDeletions
      || users.filter((u) => !u.isAdmin && u.deletionCode);
    const delCard = $('#admin-deletions-card');
    const delBox = $('#admin-deletions-list');
    if (delCard && delBox) {
      delCard.hidden = pendingDeletions.length === 0;
      if (pendingDeletions.length > 0) {
        delBox.innerHTML = pendingDeletions.map((u) => {
          const title = u.displayName || u.username;
          const reqDay = u.deletionRequestedAt ? u.deletionRequestedAt.split('T')[0] : '';
          return `
            <div class="admin-row" data-del-req-user="${u.id}">
              <div class="admin-row-icon is-danger">
                <i data-lucide="user-x"></i>
              </div>
              <div class="admin-row-main">
                <div class="admin-row-title">
                  <span class="admin-user-name">${esc(title)}</span>
                  <span class="admin-user-handle">@${esc(u.username)}</span>
                  <span class="badge badge-outline">ID: #${u.id}</span>
                  <button class="invite-code-pill" type="button" data-copy-code="${esc(u.deletionCode)}" title="Копирај потврдни код">
                    <span>КОД: ${esc(u.deletionCode)}</span>
                  </button>
                </div>
                <div class="admin-row-meta">
                  <span>Захтев генерисан${reqDay ? ' ' + esc(reqDay) : ''} · упореди са поруком на admin@crnbg.org</span>
                </div>
              </div>
              <div class="admin-row-actions">
                <button class="btn btn-outline btn-sm" type="button" data-clear-del-code="${u.id}" title="Поништи код (откључава кориснику ново генерисање)">
                  <i data-lucide="rotate-ccw"></i><span>Поништи код</span>
                </button>
                <button class="btn btn-outline btn-sm is-danger-btn" type="button" data-delete-user="${u.id}" data-delete-label="Потврди брисање" title="Потврди и трајно обриши налог">
                  <i data-lucide="trash-2"></i><span>Потврди брисање</span>
                </button>
              </div>
            </div>`;
        }).join('');
      } else {
        delBox.innerHTML = '';
      }
    }

    const invBox = $('#admin-invites-list');
    if (invBox) {
      if (!invites.length) {
        invBox.innerHTML = `
          <div class="empty" style="padding: 2.25rem 1rem;">
            <span class="empty-icon"><i data-lucide="key-round"></i></span>
            <p class="muted">Још нема генерисаних кодова за регистрацију.</p>
            <button class="btn btn-primary btn-sm" type="button" data-admin-action="generate-invite">
              <i data-lucide="plus"></i><span>Генериши први код</span>
            </button>
          </div>`;
      } else {
        invBox.innerHTML = invites.map((ic) => {
          const isUsed = Boolean(ic.usedAt || ic.usedBy);
          const createdDay = ic.createdAt ? ic.createdAt.split('T')[0] : '';
          const usedDay = ic.usedAt ? ic.usedAt.split('T')[0] : '';
          const statusBadge = isUsed
            ? '<span class="badge badge-outline"><i data-lucide="check"></i><span>Искоришћен</span></span>'
            : '<span class="badge badge-success"><i data-lucide="sparkles"></i><span>Активан</span></span>';
          const usedInfo = isUsed
            ? (ic.usedBy > 0 && ic.usedByUsername && ic.usedByUsername !== 'обрисан налог'
              ? `Искористио <button class="admin-user-link" type="button" data-open-user="${esc(ic.usedByUsername)}">@${esc(ic.usedByUsername)}</button>${usedDay ? ' · ' + esc(usedDay) : ''}`
              : `Искоришћен${ic.usedByUsername ? ' (' + esc(ic.usedByUsername) + ')' : ''}${usedDay ? ' · ' + esc(usedDay) : ''}`)
            : `Креиран ${esc(createdDay || 'данас')} · важи за 1 регистрацију`;

          return `
            <div class="admin-row ${isUsed ? 'is-used' : ''}" data-invite-id="${ic.id}">
              <div class="admin-row-icon ${isUsed ? '' : 'is-active'}">
                <i data-lucide="${isUsed ? 'check-circle-2' : 'key-round'}"></i>
              </div>
              <div class="admin-row-main">
                <div class="admin-row-title">
                  <button class="invite-code-pill" type="button" data-copy-code="${esc(ic.code)}" title="Кликни да копираш код">
                    <span>${esc(ic.code)}</span>
                  </button>
                  ${statusBadge}
                </div>
                <div class="admin-row-meta">
                  <span>${usedInfo}</span>
                </div>
              </div>
              <div class="admin-row-actions">
                ${!isUsed ? `
                  <button class="btn btn-outline btn-sm" type="button" data-copy-code="${esc(ic.code)}">
                    <i data-lucide="copy"></i><span>Копирај</span>
                  </button>
                ` : ''}
                <button class="btn btn-ghost btn-icon btn-sm" type="button" data-delete-invite="${ic.id}" title="${isUsed ? 'Уклони из евиденције' : 'Повуци код'}" aria-label="Обриши код">
                  <i data-lucide="trash-2"></i>
                </button>
              </div>
            </div>`;
        }).join('');
      }
    }
    renderAdminPager('#admin-invites-pager', '#admin-invites-pager-info', '#admin-invites-prev', '#admin-invites-next', state.admin.invitesMeta);

    const uMeta = state.admin.usersMeta;
    const subEl = $('#admin-users-sub');
    if (subEl) {
      if (state.adminQuery && uMeta) {
        subEl.textContent = `Претрага „${state.adminQuery}” — пронађено ${uMeta.total} ${plural(uMeta.total, 'налог', 'налога', 'налога')}.`;
      } else {
        subEl.textContent = 'Само први налог има администраторска права (приказ до 15 по страници).';
      }
    }
    const clearBtn = $('#admin-user-search-clear');
    if (clearBtn) clearBtn.hidden = !state.adminQuery;

    const usrBox = $('#admin-users-list');
    if (usrBox) {
      if (!users.length && state.adminQuery) {
        usrBox.innerHTML = `
          <div class="empty" style="padding: 2.25rem 1rem;">
            <span class="empty-icon"><i data-lucide="search-x"></i></span>
            <p class="muted">Нема налога за упит „<b>${esc(state.adminQuery)}</b>”.</p>
            <button class="btn btn-outline btn-sm" type="button" data-clear-admin-search>
              <i data-lucide="x"></i><span>Поништи претрагу</span>
            </button>
          </div>`;
      } else {
        usrBox.innerHTML = users.map((u, idx) => {
          const title = u.displayName || u.username;
          const admBadge = u.isAdmin
            ? '<span class="badge badge-admin"><i data-lucide="shield"></i><span>Админ</span></span>'
            : '';
          const delBadge = (!u.isAdmin && u.deletionCode)
            ? '<span class="badge badge-danger"><i data-lucide="alert-triangle"></i><span>Захтева брисање</span></span>'
            : '';
          return `
            <div class="admin-row" data-user-id="${u.id}">
              <div class="admin-user-avatar" data-admin-avatar="${idx}">
                <i data-lucide="user"></i>
              </div>
              <div class="admin-row-main">
                <div class="admin-row-title">
                  <span class="admin-user-name">${esc(title)}</span>
                  <span class="admin-user-handle">@${esc(u.username)}</span>
                  ${admBadge}
                  ${delBadge}
                </div>
                <div class="admin-row-meta">
                  <span>ID: #${u.id} · Члан од ${esc(formatMemberSince(u.memberSince))}</span>
                </div>
              </div>
              <div class="admin-row-actions">
                <button class="btn btn-outline btn-sm" type="button" data-open-user="${esc(u.username)}">
                  <i data-lucide="user"></i><span>Налог</span>
                </button>
                ${!u.isAdmin ? `
                  <button class="btn btn-ghost btn-icon btn-sm" type="button" data-delete-user="${u.id}" title="Обриши налог" aria-label="Обриши налог">
                    <i data-lucide="trash-2"></i>
                  </button>
                ` : ''}
              </div>
            </div>`;
        }).join('');

        users.forEach((u, idx) => {
          setAvatarBox($(`[data-admin-avatar="${idx}"]`, usrBox), u.avatar, u.displayName || u.username);
        });
      }
    }
    renderAdminPager('#admin-users-pager', '#admin-users-pager-info', '#admin-users-prev', '#admin-users-next', uMeta);

    icons();
  }

  async function generateInviteCode(btn) {
    if (btn) btn.disabled = true;
    try {
      const ic = await api('/api/admin/invites', { method: 'POST' });
      state.adminInvitesPage = 1;
      await copyText(ic.code, `Код копиран: ${ic.code}`);
      await loadAdminPage();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    } finally {
      if (btn) btn.disabled = false;
    }
  }

  async function deleteInviteCode(id, btn) {
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
      await api(`/api/admin/invites/${id}`, { method: 'DELETE' });
      toast('Код уклоњен');
      await loadAdminPage();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function clearUserDeletionCode(id, btn) {
    if (!btn.classList.contains('is-confirm')) {
      btn.classList.add('is-confirm');
      btn.textContent = 'Поништи код?';
      setTimeout(() => {
        if (!btn.isConnected) return;
        btn.classList.remove('is-confirm');
        btn.innerHTML = '<i data-lucide="rotate-ccw"></i><span>Поништи код</span>';
        icons();
      }, 3500);
      return;
    }
    try {
      await api(`/api/admin/users/${id}/deletion-code`, { method: 'DELETE' });
      toast('Потврдни код за брисање је поништен');
      await loadAdminPage();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function deleteUserAccount(id, btn) {
    if (!btn.classList.contains('is-confirm')) {
      const label = btn.dataset.deleteLabel;
      btn.classList.add('is-confirm');
      btn.textContent = 'Обриши налог?';
      setTimeout(() => {
        if (!btn.isConnected) return;
        btn.classList.remove('is-confirm');
        btn.innerHTML = label
          ? `<i data-lucide="trash-2"></i><span>${esc(label)}</span>`
          : '<i data-lucide="trash-2"></i>';
        icons();
      }, 3500);
      return;
    }
    try {
      await api(`/api/admin/users/${id}`, { method: 'DELETE' });
      toast('Налог и његови подаци су обрисани');
      await loadAdminPage();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  // ---------------------------------------------------------------
  // Навигација и рутирање (/, /liste, /liste/:id, /u/@korisnik, /podesavanja, /admin)
  // ---------------------------------------------------------------
  let routeSeq = 0;

  function navigate(path, push = true) {
    closeSettingsMenu();
    if (push && window.location.pathname !== path) {
      history.pushState(null, '', path);
    }
    routeTo(path);
  }

  async function routeTo(path) {
    const seq = ++routeSeq;
    closeSettingsMenu();
    if (path.length > 1 && path.endsWith('/')) path = path.slice(0, -1);

    const vDash = $('#view-dashboard');
    const vLists = $('#view-lists');
    const vDetail = $('#view-list-detail');
    const vProfile = $('#view-profile');
    const vSettings = $('#view-settings');
    const vAdmin = $('#view-admin');
    const btnLists = $('#btn-lists');

    if (path === '/admin' || path.startsWith('/admin/')) {
      if (!state.account || !state.account.isAdmin) {
        toast('Приступ је дозвољен само администратору', 'error');
        history.replaceState(null, '', '/');
        return routeTo('/');
      }
      if (vDash) vDash.hidden = true;
      if (vLists) vLists.hidden = true;
      if (vDetail) vDetail.hidden = true;
      if (vProfile) vProfile.hidden = true;
      if (vSettings) vSettings.hidden = true;
      if (vAdmin) vAdmin.hidden = false;
      if (btnLists) btnLists.classList.remove('btn-primary');
      if (btnLists) btnLists.classList.add('btn-outline');
      document.title = 'Keshk >> Админ панел';
      await Promise.all([loadAdminPage(seq), !state.data ? load() : null]);
    } else if (path === '/podesavanja' || path.startsWith('/podesavanja/')) {
      if (vDash) vDash.hidden = true;
      if (vLists) vLists.hidden = true;
      if (vDetail) vDetail.hidden = true;
      if (vProfile) vProfile.hidden = true;
      if (vAdmin) vAdmin.hidden = true;
      if (vSettings) vSettings.hidden = false;
      if (btnLists) btnLists.classList.remove('btn-primary');
      if (btnLists) btnLists.classList.add('btn-outline');
      if (window.location.pathname !== '/podesavanja') {
        history.replaceState(null, '', '/podesavanja');
      }
      document.title = 'Keshk >> Подешавања';
      renderSettingsPage();
      await Promise.all([loadSettingsPage(seq), !state.data ? load() : null]);
    } else if (path === '/liste') {
      if (vDash) vDash.hidden = true;
      if (vDetail) vDetail.hidden = true;
      if (vProfile) vProfile.hidden = true;
      if (vSettings) vSettings.hidden = true;
      if (vAdmin) vAdmin.hidden = true;
      if (vLists) vLists.hidden = false;
      if (btnLists) btnLists.classList.add('btn-primary');
      if (btnLists) btnLists.classList.remove('btn-outline');
      document.title = 'Keshk >> Листе за куповину';
      await Promise.all([loadListsPage(), !state.data ? load() : null]);
    } else if (path.startsWith('/liste/')) {
      const id = path.slice('/liste/'.length);
      if (vDash) vDash.hidden = true;
      if (vLists) vLists.hidden = true;
      if (vProfile) vProfile.hidden = true;
      if (vSettings) vSettings.hidden = true;
      if (vAdmin) vAdmin.hidden = true;
      if (vDetail) vDetail.hidden = false;
      if (btnLists) btnLists.classList.add('btn-primary');
      if (btnLists) btnLists.classList.remove('btn-outline');
      await Promise.all([loadListDetailPage(id), !state.data ? load() : null]);
    } else if (path === '/u' || path.startsWith('/u/')) {
      if (vDash) vDash.hidden = true;
      if (vLists) vLists.hidden = true;
      if (vDetail) vDetail.hidden = true;
      if (vSettings) vSettings.hidden = true;
      if (vAdmin) vAdmin.hidden = true;
      if (vProfile) vProfile.hidden = false;
      if (btnLists) btnLists.classList.remove('btn-primary');
      if (btnLists) btnLists.classList.add('btn-outline');

      let raw = '';
      if (path.startsWith('/u/')) {
        try { raw = decodeURIComponent(path.slice(3)); } catch { raw = path.slice(3); }
      }
      let uname = raw.replace(/^@+/, '').split('/')[0].trim();
      if (!uname && state.account && state.account.username) {
        uname = state.account.username;
      }
      if (uname) {
        const canonical = `/u/@${encodeURIComponent(uname)}`;
        if (window.location.pathname !== canonical) {
          history.replaceState(null, '', canonical);
        }
      }
      await Promise.all([loadProfilePage(uname, seq), !state.data ? load() : null]);
    } else {
      if (vLists) vLists.hidden = true;
      if (vDetail) vDetail.hidden = true;
      if (vProfile) vProfile.hidden = true;
      if (vSettings) vSettings.hidden = true;
      if (vAdmin) vAdmin.hidden = true;
      if (vDash) vDash.hidden = false;
      if (btnLists) btnLists.classList.remove('btn-primary');
      if (btnLists) btnLists.classList.add('btn-outline');
      document.title = 'Keshk >> месечни буџет';
      if (!state.data) await load();
    }
    if (seq !== routeSeq) return;
    icons();
    window.scrollTo({ top: 0, behavior: 'instant' });
  }

  // ---------------------------------------------------------------
  // Листе за куповину (/liste)
  // ---------------------------------------------------------------
  async function loadListsPage() {
    try {
      state.lists = await api('/api/lists');
      renderListsPageGrid();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  function renderListsPageGrid() {
    const grid = $('#lists-grid');
    if (!grid) return;
    const lists = state.lists || [];
    if (!lists.length) {
      grid.innerHTML = `
        <div class="card lists-page-empty">
          <span class="lists-page-empty-icon"><i data-lucide="list-checks"></i></span>
          <p>Још немаш ниједну листу за куповину.</p>
          <button id="btn-create-first-list" class="btn btn-primary btn-sm" type="button">
            <i data-lucide="plus"></i><span>Креирај прву листу</span>
          </button>
        </div>`;
      $('#btn-create-first-list')?.addEventListener('click', () => openListDialog());
      icons();
      return;
    }

    const todayStr = today();
    grid.innerHTML = lists.map((l, i) => {
      const pct = l.itemCount > 0 ? Math.round((l.checkedCount / l.itemCount) * 100) : 0;
      const dueLabel = l.dueDate
        ? `<span class="badge badge-outline${l.dueDate < todayStr && !l.txId ? ' is-overdue' : ''}"><i data-lucide="calendar"></i>${l.dueDate}</span>`
        : '<span class="badge badge-outline"><i data-lucide="infinity"></i>Без рока</span>';
      const kLabel = l.keshkicTitle
        ? `<span class="badge badge-outline"><i data-lucide="wallet"></i>${esc(l.keshkicTitle)}</span>`
        : '';
      const createdShort = l.createdAt ? l.createdAt.split('T')[0] : '';
      const createdLabel = createdShort
        ? `<span class="badge badge-outline" title="Креирано"><i data-lucide="clock"></i>${createdShort}</span>`
        : '';
      const chargedBadge = l.txId > 0
        ? `<span class="badge badge-success"><i data-lucide="check"></i>Одузето</span>`
        : '';

      return `
        <div class="sl-page-card${l.txId > 0 ? ' is-charged' : ''}" data-list-id="${l.id}" style="animation-delay:${i * 0.03}s">
          <div class="sl-page-card-head">
            <div class="sl-page-card-title">${esc(l.title)}</div>
            ${chargedBadge}
          </div>
          <div class="sl-page-card-meta">
            ${kLabel}
            ${createdLabel}
            ${dueLabel}
          </div>
          <div class="sl-page-card-progress">
            <div class="sl-page-card-progress-bar"><div class="sl-page-card-progress-fill" style="width:${pct}%"></div></div>
            <span>${l.checkedCount} од ${l.itemCount} купљено (${pct}%)</span>
          </div>
          <div class="sl-page-card-foot">
            <span>Укупно:</span>
            <span class="sl-page-card-total">${l.total > 0 ? money(l.total) : '0,00 RSD'}</span>
          </div>
        </div>`;
    }).join('');
    icons();
  }

  // ---------------------------------------------------------------
  // Детаљ листе (/liste/:id)
  // ---------------------------------------------------------------
  async function loadListDetailPage(id) {
    state.editingItemId = null;
    try {
      const l = await api(`/api/lists/${id}`);
      state.currentList = l;
      renderListDetailPage(l);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) {
        toast('Листа није пронађена или је обрисана', 'error');
        navigate('/liste');
      }
    }
  }

  function renderListDetailPage(l) {
    if (!l) return;
    document.title = `Keshk >> ${l.title}`;
    $('#page-list-title').textContent = l.title;

    const kBadge = l.keshkicTitle
      ? `<span class="badge badge-outline"><i data-lucide="wallet"></i>${esc(l.keshkicTitle)}</span>`
      : '<span class="badge badge-outline"><i data-lucide="wallet"></i>Подразумевани Кешкић</span>';
    const dueLabel = l.dueDate
      ? `<span class="badge badge-outline"><i data-lucide="calendar"></i>Рок: ${l.dueDate}</span>`
      : '<span class="badge badge-outline"><i data-lucide="infinity"></i>Без рока</span>';
    const createdShort = l.createdAt ? l.createdAt.split('T')[0] : '—';
    const createdBadge = `<span class="badge badge-outline"><i data-lucide="clock"></i>${createdShort}</span>`;
    const chargedBadge = l.txId > 0
      ? `<span class="badge badge-success"><i data-lucide="check-circle-2"></i>Одузето са Кешкића</span>`
      : '';

    $('#page-list-meta').innerHTML = `${kBadge} ${dueLabel} ${createdBadge} ${chargedBadge}`;

    // Items
    const items = l.items || [];
    const el = $('#page-list-items');
    if (!items.length) {
      el.innerHTML = '<p class="muted small" style="padding:2rem 0;text-align:center;">Ова листа још нема ставки. Додај први производ испод.</p>';
    } else {
      el.innerHTML = items.map(it => {
        const priceLabel = it.price != null ? `<span class="li-item-price">${nf.format(round2(it.price))} RSD</span>` : '';
        if (state.editingItemId === it.id) {
          return `
            <form class="li-item li-item-form" data-edit-form="${it.id}" novalidate>
              <input class="input" name="name" value="${esc(it.name)}" maxlength="120" autocomplete="off" required aria-label="Назив производа">
              <input class="input input-sm" name="price" value="${it.price != null ? nf.format(round2(it.price)) : ''}" inputmode="decimal" placeholder="Цена (RSD)" autocomplete="off" aria-label="Цена (RSD)">
              <button class="btn btn-primary btn-icon btn-sm" type="submit" aria-label="Сачувај измену"><i data-lucide="check"></i></button>
              <button class="btn btn-ghost btn-icon btn-sm" type="button" data-cancel-edit aria-label="Откажи"><i data-lucide="x"></i></button>
            </form>`;
        }
        return `
          <div class="li-item${it.checked ? ' is-checked' : ''}" data-item-id="${it.id}">
            <input type="checkbox" ${it.checked ? 'checked' : ''} data-toggle-item="${it.id}" aria-label="Купљено">
            <span class="li-item-name">${esc(it.name)}</span>
            ${priceLabel}
            <button class="li-item-edit" type="button" data-edit-item="${it.id}" aria-label="Измени производ" title="Измени"><i data-lucide="pencil"></i></button>
            <button class="li-item-delete" type="button" data-delete-item="${it.id}" aria-label="Обриши производ"><i data-lucide="trash-2"></i></button>
          </div>`;
      }).join('');
    }

    // Summary
    const sum = $('#page-list-summary');
    sum.innerHTML = `
      <span>Купљено: <b>${l.checkedCount}</b> од <b>${l.itemCount}</b> производа</span>
      <span class="muted">Укупно: <b>${money(l.total)}</b>${l.totalAll > l.total ? ` (сви производи: ${money(l.totalAll)})` : ''}</span>`;

    // Charge buttons
    $('#page-list-charge-btn').hidden = l.txId > 0;
    $('#page-list-uncharge-btn').hidden = !(l.txId > 0);
    icons();
  }

  async function refreshListDetailPage(id) {
    try {
      const l = await api(`/api/lists/${id}`);
      state.currentList = l;
      renderListDetailPage(l);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  // --- Create / Edit list dialog ---
  function openListDialog(list = null) {
    closeSettingsMenu();
    state.editingList = list;
    const d = $('#list-dialog');
    $('#list-dialog-title').textContent = list ? 'Измени листу' : 'Нова листа';
    $('#list-save-label').textContent = list ? 'Сачувај измене' : 'Сачувај';
    $('#list-title').value = list ? list.title : '';
    $('#list-due').value = list ? list.dueDate : '';
    $('#list-error').hidden = true;
    populateKeshkicSelect('#list-keshkic', list ? list.keshkicId : 0);
    d.showModal();
    icons();
    setTimeout(() => $('#list-title').focus(), 60);
  }

  function populateKeshkicSelect(selector, selectedId) {
    const select = $(selector);
    const keshkici = state.data && state.data.summary ? (state.data.summary.keshkici || []).filter(k => !k.dropped) : [];
    select.innerHTML = '<option value="0">— није изабран (подразумевани) —</option>' +
      keshkici.map(k => `<option value="${k.id}">${esc(k.title)} (преостало: ${money(k.balance)})${k.isDefault ? ' [подразумевани]' : ''}</option>`).join('');
    if (selectedId) select.value = String(selectedId);
  }

  async function saveList(e) {
    e.preventDefault();
    const err = $('#list-error');
    err.hidden = true;
    const title = $('#list-title').value.trim();
    if (!title) { err.textContent = 'Унеси назив листе.'; err.hidden = false; return; }
    const body = {
      title,
      keshkicId: Number($('#list-keshkic').value) || 0,
      dueDate: $('#list-due').value || '',
      currency: 'RSD',
    };
    try {
      if (state.editingList) {
        await api(`/api/lists/${state.editingList.id}`, { method: 'PUT', body });
        toast('Листа измењена');
        $('#list-dialog').close();
        if (state.currentList && state.currentList.id === state.editingList.id) {
          await refreshListDetailPage(state.editingList.id);
        } else {
          await loadListsPage();
        }
      } else {
        const created = await api('/api/lists', { method: 'POST', body });
        toast('Листа креирана');
        $('#list-dialog').close();
        navigate(`/liste/${created.id}`);
      }
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) { err.textContent = ex.message; err.hidden = false; }
    }
  }

  async function toggleItem(listId, itemId, item, checked) {
    try {
      const body = { name: item.name, price: item.price, checked };
      await api(`/api/lists/${listId}/items/${itemId}`, { method: 'PUT', body });
      await refreshListDetailPage(listId);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function addItemToList(e) {
    e.preventDefault();
    if (!state.currentList) return;
    const name = $('#page-add-item-name').value.trim();
    if (!name) return;
    const priceRaw = parseAmount($('#page-add-item-price').value);
    const price = priceRaw > 0 ? priceRaw : null;
    try {
      await api(`/api/lists/${state.currentList.id}/items`, { method: 'POST', body: { name, price } });
      $('#page-add-item-name').value = '';
      $('#page-add-item-price').value = '';
      await refreshListDetailPage(state.currentList.id);
      setTimeout(() => $('#page-add-item-name').focus(), 60);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  function startEditItem(itemId) {
    if (!state.currentList) return;
    state.editingItemId = itemId;
    renderListDetailPage(state.currentList);
    const input = $(`[data-edit-form="${itemId}"] input[name="name"]`);
    if (input) { input.focus(); input.select(); }
  }

  function cancelEditItem() {
    if (state.editingItemId == null || !state.currentList) return;
    state.editingItemId = null;
    renderListDetailPage(state.currentList);
  }

  async function saveItemEdit(e) {
    e.preventDefault();
    const form = e.target.closest('[data-edit-form]');
    if (!form || !state.currentList) return;
    const itemId = Number(form.dataset.editForm);
    const item = (state.currentList.items || []).find((i) => i.id === itemId);
    if (!item) return;

    const name = form.elements.name.value.trim();
    if (!name) { toast('Унеси назив производа', 'error'); form.elements.name.focus(); return; }
    const rawPrice = form.elements.price.value.trim();
    let price = null;
    if (rawPrice) {
      price = parseAmount(rawPrice);
      if (!(price >= 0)) { toast('Неисправна цена, нпр. 250,00', 'error'); form.elements.price.focus(); return; }
      if (price === 0) price = null;
    }
    try {
      await api(`/api/lists/${state.currentList.id}/items/${itemId}`, { method: 'PUT', body: { name, price, checked: item.checked } });
      state.editingItemId = null;
      await refreshListDetailPage(state.currentList.id);
      toast('Производ измењен');
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function deleteItem(listId, itemId) {
    try {
      await api(`/api/lists/${listId}/items/${itemId}`, { method: 'DELETE' });
      await refreshListDetailPage(listId);
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function chargeList() {
    if (!state.currentList) return;
    try {
      await api(`/api/lists/${state.currentList.id}/charge`, { method: 'POST' });
      toast('Одузето од Кешкића');
      await refreshListDetailPage(state.currentList.id);
      if (state.data) await load();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function unchargeList() {
    if (!state.currentList) return;
    try {
      await api(`/api/lists/${state.currentList.id}/charge`, { method: 'DELETE' });
      toast('Одузимање поништено');
      await refreshListDetailPage(state.currentList.id);
      if (state.data) await load();
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
  }

  async function deleteList() {
    if (!state.currentList) return;
    if (!confirm('Да ли си сигуран да желиш да обришеш ову листу?')) return;
    try {
      await api(`/api/lists/${state.currentList.id}`, { method: 'DELETE' });
      toast('Листа обрисана');
      state.currentList = null;
      navigate('/liste');
    } catch (ex) {
      if (!(ex instanceof Unauthorized)) toast(ex.message, 'error');
    }
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
        resetClientState();
        await showApp(await api('/api/me'));
      } catch (ex) {
        err.textContent = ex.message;
        err.hidden = false;
      } finally {
        btn.disabled = false;
      }
    });

    // Регистрација (позивни код + Onboarding)
    $('#btn-show-register')?.addEventListener('click', () => openRegisterCard());
    $('#btn-back-to-login')?.addEventListener('click', closeRegisterCard);
    $('#btn-ob-back-code')?.addEventListener('click', () => showOnboardingStep('code'));
    $('#btn-ob-back-account')?.addEventListener('click', () => showOnboardingStep('account'));

    $('#reg-code')?.addEventListener('input', (e) => {
      e.target.value = formatInviteCodeInput(e.target.value);
    });

    $('#reg-code-form')?.addEventListener('submit', async (e) => {
      e.preventDefault();
      const err = $('#reg-code-error');
      const btn = $('#reg-code-submit');
      err.hidden = true;
      const code = ($('#reg-code').value || '').trim();
      if (!code) {
        err.textContent = 'Унеси код за регистрацију.';
        err.hidden = false;
        return;
      }
      btn.disabled = true;
      try {
        const res = await api('/api/register/verify-code', { method: 'POST', body: { code } });
        state.onboarding.code = res.code;
        $('#reg-code').value = res.code;
        showOnboardingStep('account');
      } catch (ex) {
        err.textContent = ex.message;
        err.hidden = false;
      } finally {
        btn.disabled = false;
      }
    });

    $('#reg-account-form')?.addEventListener('submit', async (e) => {
      e.preventDefault();
      const err = $('#reg-account-error');
      const btn = $('#reg-account-next');
      err.hidden = true;
      const failStep = (msg) => { err.textContent = msg; err.hidden = false; };

      const username = ($('#reg-username').value || '').trim().replace(/^@+/, '');
      const displayName = ($('#reg-display-name').value || '').trim();
      const pw = $('#reg-password').value || '';
      const pw2 = $('#reg-password-confirm').value || '';

      if (username.length < 2) return failStep('Унеси корисничко име (бар 2 знака).');
      if (pw.length < 6) return failStep('Лозинка мора имати бар 6 знакова.');
      if (pw !== pw2) return failStep('Лозинке се не поклапају.');

      btn.disabled = true;
      try {
        const res = await api('/api/register/check-username', {
          method: 'POST',
          body: { code: state.onboarding.code, username },
        });
        state.onboarding.username = res.username;
        state.onboarding.displayName = displayName;
        state.onboarding.password = pw;
        $('#reg-username').value = res.username;
        showOnboardingStep('profile');
      } catch (ex) {
        failStep(ex.message);
      } finally {
        btn.disabled = false;
      }
    });

    $('#reg-avatar')?.addEventListener('input', updateOnboardingPreview);
    $('#reg-banner')?.addEventListener('input', updateOnboardingPreview);

    $('#reg-profile-form')?.addEventListener('submit', async (e) => {
      e.preventDefault();
      const err = $('#reg-profile-error');
      const btn = $('#reg-finish-submit');
      err.hidden = true;
      const failStep = (msg) => { err.textContent = msg; err.hidden = false; };

      const avatar = ($('#reg-avatar').value || '').trim();
      const banner = ($('#reg-banner').value || '').trim();
      const bio = ($('#reg-bio').value || '').trim();

      if (avatar && !isHttpUrl(avatar)) return failStep('Профилна слика мора почињати са https:// или http://');
      if (banner && !isHttpUrl(banner)) return failStep('Позадина мора почињати са https:// или http://');

      btn.disabled = true;
      try {
        const me = await api('/api/register', {
          method: 'POST',
          body: {
            code: state.onboarding.code,
            username: state.onboarding.username,
            password: state.onboarding.password,
            displayName: state.onboarding.displayName,
            avatar,
            banner,
            bio,
          },
        });
        resetOnboardingForms();
        $('#register-card').hidden = true;
        $('#login-form').hidden = false;
        if (window.location.search) history.replaceState(null, '', window.location.pathname);
        resetClientState();
        await showApp(me);
        toast(`Добродошао, @${me.username}!`);
      } catch (ex) {
        failStep(ex.message);
      } finally {
        btn.disabled = false;
      }
    });

    // Подешавања дропдаун
    $('.topbar')?.addEventListener('dragstart', (e) => e.preventDefault());
    $('#settings-trigger').addEventListener('click', toggleSettingsMenu);
    document.addEventListener('click', (e) => {
      if (!e.target.closest('#settings-dropdown')) closeSettingsMenu();
    });
    $('#menu-theme').addEventListener('click', () => { toggleTheme(); closeSettingsMenu(); });
    $('#menu-logout').addEventListener('click', async () => {
      closeSettingsMenu();
      await api('/api/logout', { method: 'POST' }).catch(() => {});
      resetClientState();
      resetOnboardingForms();
      if (window.location.pathname !== '/') history.replaceState(null, '', '/');
      showLogin({ manualLogout: true });
    });
    $('#menu-account').addEventListener('click', openMyProfile);
    $('#menu-profile-header')?.addEventListener('click', openMyProfile);
    $('#menu-settings')?.addEventListener('click', () => navigate('/podesavanja'));
    $('#menu-admin')?.addEventListener('click', () => navigate('/admin'));
    $('#menu-export').addEventListener('click', exportData);
    $('#menu-import').addEventListener('click', () => { closeSettingsMenu(); $('#import-input').click(); });
    $('#import-input').addEventListener('change', (e) => {
      const file = e.target.files[0];
      e.target.value = '';
      importData(file);
    });

    // Страница Подешавања (/podesavanja)
    $('#back-from-settings')?.addEventListener('click', () => navigate('/'));
    $('#settings-edit-account')?.addEventListener('click', openProfileDialog);
    $('#settings-change-pw')?.addEventListener('click', openPasswordDialog);
    $('#settings-export')?.addEventListener('click', exportData);
    $('#settings-import')?.addEventListener('click', () => $('#import-input').click());
    $('#clear-opt-keshkici')?.addEventListener('change', syncClearCheckboxes);
    $('#clear-opt-lists')?.addEventListener('change', syncClearCheckboxes);
    $('#clear-opt-txs')?.addEventListener('change', syncClearCheckboxes);
    $('#btn-clear-data')?.addEventListener('click', (e) => submitClearData(e.currentTarget));
    $('#settings-deletion-body')?.addEventListener('click', (e) => {
      const genBtn = e.target.closest('#btn-gen-deletion-code');
      if (genBtn) return generateDeletionCode(genBtn);
      const copyPromptBtn = e.target.closest('[data-copy-deletion-prompt]');
      if (copyPromptBtn && state.account) {
        return copyText(buildDeletionPrompt(state.account), 'Захтев за брисање налога је копиран');
      }
      if (e.target.closest('[data-go-admin]')) return navigate('/admin');
    });

    // Админ панел (/admin) — претрага и пагинација (15 по страници)
    const resetAdminSearch = () => {
      if (adminSearchTimer) clearTimeout(adminSearchTimer);
      state.adminQuery = '';
      state.adminUsersPage = 1;
      if ($('#admin-user-search')) $('#admin-user-search').value = '';
      if ($('#admin-user-search-clear')) $('#admin-user-search-clear').hidden = true;
      loadAdminPage();
    };
    $('#admin-user-search')?.addEventListener('input', (e) => {
      const val = (e.target.value || '').trim();
      if ($('#admin-user-search-clear')) $('#admin-user-search-clear').hidden = !val;
      if (adminSearchTimer) clearTimeout(adminSearchTimer);
      adminSearchTimer = setTimeout(() => {
        if (state.adminQuery === val && state.adminUsersPage === 1) return;
        state.adminQuery = val;
        state.adminUsersPage = 1;
        loadAdminPage();
      }, 220);
    });
    $('#admin-user-search-clear')?.addEventListener('click', () => {
      resetAdminSearch();
      $('#admin-user-search')?.focus();
    });
    $('#admin-users-prev')?.addEventListener('click', () => {
      if (state.adminUsersPage > 1) {
        state.adminUsersPage--;
        loadAdminPage();
      }
    });
    $('#admin-users-next')?.addEventListener('click', () => {
      const tp = state.admin && state.admin.usersMeta ? state.admin.usersMeta.totalPages : 1;
      if (state.adminUsersPage < tp) {
        state.adminUsersPage++;
        loadAdminPage();
      }
    });
    $('#admin-invites-prev')?.addEventListener('click', () => {
      if (state.adminInvitesPage > 1) {
        state.adminInvitesPage--;
        loadAdminPage();
      }
    });
    $('#admin-invites-next')?.addEventListener('click', () => {
      const tp = state.admin && state.admin.invitesMeta ? state.admin.invitesMeta.totalPages : 1;
      if (state.adminInvitesPage < tp) {
        state.adminInvitesPage++;
        loadAdminPage();
      }
    });

    $('#back-from-admin')?.addEventListener('click', () => navigate('/'));
    $('#btn-generate-invite')?.addEventListener('click', (e) => generateInviteCode(e.currentTarget));
    $('#btn-generate-invite-card')?.addEventListener('click', (e) => generateInviteCode(e.currentTarget));
    $('#view-admin')?.addEventListener('click', (e) => {
      if (e.target.closest('[data-clear-admin-search]')) return resetAdminSearch();
      const genBtn = e.target.closest('[data-admin-action="generate-invite"]');
      if (genBtn) return generateInviteCode(genBtn);
      const copyBtn = e.target.closest('[data-copy-code]');
      if (copyBtn) return copyText(copyBtn.dataset.copyCode, `Код копиран: ${copyBtn.dataset.copyCode}`);
      const delInvBtn = e.target.closest('[data-delete-invite]');
      if (delInvBtn) return deleteInviteCode(Number(delInvBtn.dataset.deleteInvite), delInvBtn);
      const openUserBtn = e.target.closest('[data-open-user]');
      if (openUserBtn) return navigate(`/u/@${encodeURIComponent(openUserBtn.dataset.openUser)}`);
      const clrDelBtn = e.target.closest('[data-clear-del-code]');
      if (clrDelBtn) return clearUserDeletionCode(Number(clrDelBtn.dataset.clearDelCode), clrDelBtn);
      const delUserBtn = e.target.closest('[data-delete-user]');
      if (delUserBtn) return deleteUserAccount(Number(delUserBtn.dataset.deleteUser), delUserBtn);
    });

    // Налог и лозинка
    $('#profile-form').addEventListener('submit', saveProfile);
    $('#prof-avatar').addEventListener('input', updateProfilePreview);
    $('#prof-banner').addEventListener('input', updateProfilePreview);
    $('#password-form').addEventListener('submit', changePassword);
    $('#back-from-profile')?.addEventListener('click', () => navigate('/'));
    $('#profile-container')?.addEventListener('click', async (e) => {
      if (e.target.closest('#btn-notfound-home')) return navigate('/');
      if (e.target.closest('#btn-qr-profile') && state.currentProfile) {
        return openProfileQRDialog(state.currentProfile);
      }
      const copyBtn = e.target.closest('#btn-copy-profile');
      if (copyBtn && state.currentProfile) {
        const url = `${window.location.origin}/u/@${encodeURIComponent(state.currentProfile.username)}`;
        await copyText(url, 'Линк налога копиран');
        if (copyBtn.isConnected) {
          copyBtn.innerHTML = '<i data-lucide="check"></i>';
          icons();
          setTimeout(() => {
            if (!copyBtn.isConnected) return;
            copyBtn.innerHTML = '<i data-lucide="link-2"></i>';
            icons();
          }, 1400);
        }
      }
    });
    $('#qr-copy-link')?.addEventListener('click', async () => {
      const url = $('#qr-user-url')?.dataset.url;
      if (url) await copyText(url, 'Линк налога копиран');
    });

    // Навигација
    $('a.brand').addEventListener('click', (e) => {
      e.preventDefault();
      navigate('/');
    });
    $('#btn-lists').addEventListener('click', () => {
      if (window.location.pathname === '/liste') navigate('/');
      else navigate('/liste');
    });
    $('#back-from-lists')?.addEventListener('click', () => navigate('/'));
    $('#btn-back-lists')?.addEventListener('click', () => navigate('/liste'));

    // Страница Листе (/liste)
    $('#btn-new-list-page')?.addEventListener('click', () => openListDialog());
    $('#list-form').addEventListener('submit', saveList);
    $('#lists-grid')?.addEventListener('click', (e) => {
      const card = e.target.closest('.sl-page-card');
      if (card) navigate(`/liste/${card.dataset.listId}`);
    });

    // Страница Детаљ листе (/liste/:id)
    $('#page-add-item-form')?.addEventListener('submit', addItemToList);
    $('#page-list-items')?.addEventListener('change', (e) => {
      const cb = e.target.closest('[data-toggle-item]');
      if (!cb || !state.currentList) return;
      const itemId = Number(cb.dataset.toggleItem);
      const item = (state.currentList.items || []).find(i => i.id === itemId);
      if (item) toggleItem(state.currentList.id, itemId, item, cb.checked);
    });
    $('#page-list-items')?.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-delete-item]');
      if (btn && state.currentList) deleteItem(state.currentList.id, Number(btn.dataset.deleteItem));
    });
    $('#page-list-items')?.addEventListener('click', (e) => {
      const edit = e.target.closest('[data-edit-item]');
      if (edit) return startEditItem(Number(edit.dataset.editItem));
      if (e.target.closest('[data-cancel-edit]')) cancelEditItem();
    });
    $('#page-list-items')?.addEventListener('dblclick', (e) => {
      const name = e.target.closest('.li-item-name');
      const row = name && name.closest('[data-item-id]');
      if (row) startEditItem(Number(row.dataset.itemId));
    });
    $('#page-list-items')?.addEventListener('submit', saveItemEdit);
    $('#page-list-items')?.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && e.target.closest('[data-edit-form]')) { e.preventDefault(); cancelEditItem(); }
    });
    $('#page-list-charge-btn')?.addEventListener('click', chargeList);
    $('#page-list-uncharge-btn')?.addEventListener('click', unchargeList);
    $('#page-list-delete-btn')?.addEventListener('click', deleteList);
    $('#page-list-edit-btn')?.addEventListener('click', () => {
      if (state.currentList) openListDialog(state.currentList);
    });

    $('#month-prev').addEventListener('click', () => goMonth(shiftMonth(state.month, -1)));
    $('#month-next').addEventListener('click', () => goMonth(shiftMonth(state.month, 1)));
    $('#month-label').addEventListener('click', () => goMonth(currentMonth()));

    // Главно дугме у горњој траци: "Ново" [N]
    $('#btn-new').addEventListener('click', () => $('#fab').click());
    $('#fab').addEventListener('click', () => {
      if (window.location.pathname.startsWith('/liste')) openListDialog();
      else openModal('expense');
    });

    // Преклопник у дијалогу (Трошак / Уплата / Кешкић)
    $$('input[name="modal-kind"]').forEach((r) => {
      r.addEventListener('change', () => {
        $('#tx-error').hidden = true;
        updateModalFields(r.value, state.editingTx || state.editingDeposit || state.editingKeshkic);
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

    // Избор Кешкића за преостали износ
    $('#balance-settings').addEventListener('click', () => {
      closeSettingsMenu();
      $('#balance-dialog').showModal();
      icons();
    });
    $('#balance-keshkici').addEventListener('click', (e) => {
      const option = e.target.closest('[data-balance-keshkic]');
      if (!option) return;
      state.balanceKeshkicId = Number(option.dataset.balanceKeshkic) || 0;
      if (state.account) {
        localStorage.setItem(`keshk-balance-keshkic-${state.account.id}`, String(state.balanceKeshkicId));
      }
      renderBalance();
      $('#balance-dialog').close();
    });

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
        if (act === 'details') return showKeshkicDetails(k);
        if (act === 'set-default') return setDefaultKeshkic(id);
        if (act === 'deposit') return openModal('deposit', null, { keshkicId: id });
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

    // Кликови на трансакције (трошкови и уплате)
    const findTxItem = (row) => {
      const id = Number(row.dataset.id);
      const kind = row.dataset.kind === 'deposit' ? 'deposit' : 'expense';
      const source = kind === 'deposit' ? state.data.deposits : state.data.transactions;
      return { id, kind, item: (source || []).find((x) => x.id === id) };
    };

    $('#tx-list').addEventListener('click', (e) => {
      if (e.target.closest('[data-action="new-tx"]')) return openModal('expense');
      const actionBtn = e.target.closest('[data-tx-action]');
      const row = e.target.closest('.tx');
      if (!row) return;
      const { id, kind, item } = findTxItem(row);

      if (actionBtn) {
        const act = actionBtn.dataset.txAction;
        if (act === 'edit') return openModal(kind, item);
        if (act === 'delete') return kind === 'deposit' ? deleteDeposit(id, actionBtn) : deleteTx(id, actionBtn);
        return;
      }

      $$('.tx.is-open').forEach((r) => r !== row && r.classList.remove('is-open'));
      row.classList.toggle('is-open');
    });

    $('#tx-list').addEventListener('dblclick', (e) => {
      const row = e.target.closest('.tx');
      if (row && !e.target.closest('[data-tx-action]')) {
        const { kind, item } = findTxItem(row);
        openModal(kind, item);
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
      if ($('#app-view').hidden) return;
      if (e.key === 'Escape' && !$('#settings-menu').hidden) {
        closeSettingsMenu();
        return;
      }
      if (e.target.closest('input, textarea, select, [contenteditable]') || e.metaKey || e.ctrlKey || e.altKey) return;
      if (document.querySelector('dialog[open]')) return;
      const k = e.key.toLowerCase();
      if (k === 'n' || k === 'н') { e.preventDefault(); openModal('expense'); }
      else if (k === 'k' || k === 'к') { e.preventDefault(); openModal('keshkic'); }
      else if (k === 'u' || k === 'у') { e.preventDefault(); openModal('deposit'); }
      else if (k === 'l' || k === 'л') {
        e.preventDefault();
        if (window.location.pathname === '/liste') navigate('/');
        else navigate('/liste');
      }
      else if (e.key === 'ArrowLeft') goMonth(shiftMonth(state.month, -1));
      else if (e.key === 'ArrowRight') goMonth(shiftMonth(state.month, 1));
      else if (k === 't' || k === 'т') goMonth(currentMonth());
    });

    window.addEventListener('popstate', () => {
      routeTo(window.location.pathname);
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
