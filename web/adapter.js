/* adapter.js — 把原型 v2.1 接到 i3-composer 后端。
 *
 * 原则（见 docs/SPEC-frontend-wiring.md）：
 *   1. 页面上的每一项账本状态都来自 /api/state + /api/overview，每次全量重放；
 *   2. 每个写操作都是一个真实请求，成功后再 refreshAll；绝不做本地乐观更新——
 *      否则越权时界面已经变了，而账本里什么也没发生；
 *   3. SSE 只说明「有变化了」，不携带账本内容，收到后仍是重新 GET；
 *   4. 原型专有、后端没有的东西（BRD 文稿、代码内容、Task 细项、在线编辑）
 *      不伪造：相关按钮改成如实提示「未接入」，见 spec §7。
 *
 * 本文件加载在原型主 <script> 之后：原型把函数和 var DB / BACKLOG 挂在全局，
 * 这里覆盖它们，让原型自带的 renderXxx 直接吃真实投影。
 */
(function () {
  'use strict';

  var API = '';   // 同源
  var PBC = 'fee'; // 单 PBC 阶段：种子只有 fee

  var origRefreshAll = window.refreshAll;
  var origRenderStage = window.renderStage;

  /* ---------- 通用请求 ---------- */

  function api(path, opts) {
    var init = Object.assign({ headers: { 'Content-Type': 'application/json' } }, opts || {});
    return fetch(API + path, init).then(function (r) {
      return r.text().then(function (txt) {
        var j = null;
        try { j = txt ? JSON.parse(txt) : null; } catch (e) { /* 非 JSON：保留 null */ }
        if (!r.ok) {
          var err = new Error((j && j.error) || ('请求失败（HTTP ' + r.status + '）'));
          err.status = r.status;
          throw err;
        }
        return j;
      });
    });
  }

  function fail(e) {
    window.toast(e && e.message ? e.message : '请求失败', 'err');
  }

  /* ---------- 后端投影 → 原型数据结构 ---------- */

  var ART_ORDER = ['prd', 'proto', 'ad', 'at', 'backlog', 'code'];

  function emptyVersion() {
    // 唯一用途：某个制品线在账本里还一条版本都没有时，页面不崩。
    // 它不参与计数（drafts / stale 都要求真实版本），只用于占位显示。
    return { v: '—', at: '', by: '—', source: '—', df: [], st: 'accepted', note: '（尚无版本）', placeholder: true };
  }

  function normalizeVersions(src) {
    var out = {};
    ART_ORDER.forEach(function (t) {
      var arr = (src && src[t]) || [];
      out[t] = arr.map(function (v) {
        return Object.assign({}, v, {
          df: Array.isArray(v.df) ? v.df : [],
          note: v.note || '',
          source: v.source || '',
          st: v.st || 'accepted',
        });
      });
      if (!out[t].length) out[t] = [emptyVersion()];
    });
    // 后端若将来多出别的制品类型，原样带上，不做丢弃。
    Object.keys(src || {}).forEach(function (t) {
      if (!(t in out)) out[t] = src[t] || [];
    });
    return out;
  }

  function execOf(changes) {
    var seen = [], names = [];
    changes.forEach(function (c) {
      var w = window.workerOf ? window.workerOf(c.t) : null;
      var n = w ? w.n : 'X 团队';
      if (seen.indexOf(n) < 0) { seen.push(n); names.push(n); }
    });
    return names.join(' · ') || 'X 团队';
  }

  // 原型的 Commit 比后端多两个字段：changes（本次变更集）与 exec（执行者）。
  // 它们都能从树快照推出来，所以不新增后端接口，只是在这里补全。
  function normalizeCommits(list) {
    var cs = (list || []).map(function (c) {
      return Object.assign({}, c, {
        tree: c.tree || {},
        digest: c.digest || null,
        tag: c.tag || null,
        msg: c.msg || '',
        by: c.by || '—',
        at: c.at || '',
      });
    });
    cs.forEach(function (c, i) {
      var older = cs[i + 1];
      c.changes = ART_ORDER.filter(function (t) {
        var to = c.tree[t] || '';
        var from = (older && older.tree[t]) || '';
        return to !== from;
      }).map(function (t) {
        return { t: t, from: (older && older.tree[t]) || '—', to: c.tree[t] || '—' };
      });
      if (!c.exec) c.exec = execOf(c.changes);
    });
    if (!cs.length) {
      // 账本为空时的显示占位：让人看到「没有提交」，而不是页面白屏。
      cs.push({ no: '—', type: 'new', msg: '（账本中还没有提交）', by: '—', exec: '—',
        at: '', changes: [], tree: {}, digest: null, tag: null, placeholder: true });
    }
    return cs;
  }

  function normalizeDeploys(list) {
    return (list || []).map(function (d) {
      return { tag: d.tag, commit: d.commit, url: d.url, at: d.at, by: d.by, status: d.status };
    });
  }

  function normalizeAnnos(list) {
    return (list || []).map(function (a) {
      return { x: +a.x || 0, y: +a.y || 0, w: +a.w || 0, h: +a.h || 0,
        text: a.text || '', at: a.at || '', by: a.by || '', round: a.round || '' };
    });
  }

  function normalizeItem(it) {
    var x = Object.assign({}, it, {
      due: '—',          // 原型独有：后端暂不产出截止时间
      interventions: [], // 原型独有：后端审计信息在 /api/items/{id} 的 facts 里
    });
    if (x.steps && x.steps.length) {
      x.steps = x.steps.map(function (s) {
        return Object.assign({}, s, { at: s.at || '' });
      });
    }
    // 原型执行器把 run 也暴露成 state；新拉到的对象也要有这个别名。
    if (!Object.getOwnPropertyDescriptor(x, 'state')) {
      Object.defineProperty(x, 'state', {
        get: function () { return this.run; },
        set: function (v) { this.run = v; },
      });
    }
    return x;
  }

  function applyToPrototype(st, ov) {
    var p = window.DB.pbcs[PBC];
    if (!p) {
      p = { id: PBC, name: PBC, code: 'PBC-' + PBC.toUpperCase(), line: '' };
      window.DB.pbcs[PBC] = p;
    }
    p.id = PBC;
    p.name = st.name || p.name || PBC;
    p.code = p.code || ('PBC-' + PBC.toUpperCase());
    p.versions = normalizeVersions(st.versions);
    p.commits = normalizeCommits(st.commits);
    p.deploys = normalizeDeploys(st.deploys);
    p.annos = normalizeAnnos(st.annos);
    var head = p.commits[0];
    if (head && head.no && head.no !== '—') {
      p.line = head.no.split('.').slice(0, 2).join('.');
    } else if (!p.line) {
      p.line = '—';
    }

    // 单 PBC 阶段：其它 PBC（原型里的 ticket 演示数据）不是账本的一部分，移除。
    Object.keys(window.DB.pbcs).forEach(function (id) {
      if (id !== PBC) delete window.DB.pbcs[id];
    });
    if (window.S.pbcId !== PBC) window.S.pbcId = PBC;
    if (!p.commits.some(function (c) { return c.no === window.S.selCommit; })) {
      window.S.selCommit = p.commits[0].no;
    }

    // 工作项：由后端 items 重建整张表（同一张表，人 / X 同表）。
    var items = ((ov && ov.items) || []).slice().sort(function (a, b) {
      return String(a.id).localeCompare(String(b.id));
    });
    window.BACKLOG.length = 0;
    items.forEach(function (it) { window.BACKLOG.push(normalizeItem(it)); });

    // 角色来自服务端会话 cookie；前端 S.me 只是显示镜像。
    if (ov && ov.role && window.ROLES[ov.role]) window.S.me = ov.role;

    // 原型里「派发中」的本地动画状态不是账本内容，清掉。
    window.S.dispatch = null;
  }

  /* ---------- 先拉后端，再渲染 ---------- */

  var inflight = null;
  var pending = false;
  var lastError = '';

  function syncFromServer() {
    return Promise.all([
      api('/api/state?pbc=' + encodeURIComponent(PBC)),
      api('/api/overview?pbc=' + encodeURIComponent(PBC)),
    ]).then(function (res) {
      applyToPrototype(res[0], res[1]);
      lastError = '';
      origRefreshAll.call(window);
    });
  }

  function refreshAll() {
    if (inflight) { pending = true; return inflight; }
    inflight = syncFromServer()
      .catch(function (e) {
        // 拉不到就保留上一份投影，并且只提示一次（SSE 断线时不会刷屏）。
        if (e.message !== lastError) {
          lastError = e.message;
          window.toast('拉取账本失败：' + e.message, 'err');
        }
      })
      .then(function () {
        inflight = null;
        if (pending) { pending = false; refreshAll(); }
      });
    return inflight;
  }

  /* ---------- 写操作：建、发起、拍板 ---------- */

  // 发起交付：DA（build）权限。原型里被 launchStep / rerunItem 等调用，
  // 接后端后本地七步模拟必须停用，只留这一个真实入口。
  window.runItem = async function (id) {
    try {
      await api('/api/items/' + encodeURIComponent(id) + '/run?pbc=' + encodeURIComponent(PBC),
        { method: 'POST' });
      window.toast(id + ' 已发起', 'ok');
    } catch (e) {
      fail(e);
    }
    return refreshAll();
  };

  // 重新发起（受阻项）。后端允许 blocked / idea 重新发起，语义一致。
  window.rerunItem = window.runItem;

  // 发布拍板：SA（tag）权限。原型 publishItem 是本地模拟，整体替换。
  window.publishItem = async function (id) {
    try {
      await api('/api/items/' + encodeURIComponent(id) + '/answer?pbc=' + encodeURIComponent(PBC), {
        method: 'POST',
        body: JSON.stringify({ approve: true }),
      });
      window.toast(id + ' 已提交发布决定', 'ok');
    } catch (e) {
      fail(e);
    }
    return refreshAll();
  };

  // 退回修改：同一接口的 approve:false。原型 renderItem 里的「退回修改」按钮。
  window.rejectItem = async function (id, note) {
    try {
      await api('/api/items/' + encodeURIComponent(id) + '/answer?pbc=' + encodeURIComponent(PBC), {
        method: 'POST',
        body: JSON.stringify({ approve: false, note: note || '' }),
      });
      window.toast(id + ' 发布已拒绝', 'warn');
    } catch (e) {
      fail(e);
    }
    return refreshAll();
  };
  window.rejectPublish = window.rejectItem; // spec 里的别名

  // 建 backlog：BA（backlog）权限。
  // quiet=true 时由调用方统一提示（避免一次操作弹两条 toast）。
  window.createTask = async function (title, type, env, quiet) {
    try {
      var j = await api('/api/tasks?pbc=' + encodeURIComponent(PBC), {
        method: 'POST',
        body: JSON.stringify({
          title: title,
          type: type || 'feature',
          env: env || 'test',
        }),
      });
      if (!quiet) window.toast('已创建 ' + j.id, 'ok');
      return j;
    } catch (e) {
      fail(e);
      return null;
    } finally {
      refreshAll();
    }
  };

  // 需求池底部的「提交并直通」：建任务走真实接口。
  // 发起交付是另一个权限（DA），所以这里不顺手替人发起——越权的活交给后端拒绝太晚了。
  window.submitItem = async function () {
    var input = document.getElementById('cr-title');
    var typeSel = document.getElementById('cr-type');
    var envSel = document.getElementById('cr-env');
    var title = input ? String(input.value || '').trim() : '';
    if (!title) { window.toast('先写一句要做什么', 'err'); return; }
    var j = await window.createTask(title,
      typeSel ? typeSel.value : 'feature',
      envSel ? envSel.value : 'test', true);
    if (!j) return;
    if (input) input.value = '';
    window.toast('已创建 ' + j.id + '；发起交付请切到 DA 点「发起直通」', 'ok');
  };

  // 「发起变更」弹窗：后端没有「变更意图 → 制品草稿」的接口（spec §7 范围外），
  // 但它确实产生了要做的事，所以落到同一张 backlog 上，不假装 Agent 已经在写制品。
  window.runTrigger = function () {
    var typeSel = document.getElementById('tg-type');
    var noteEl = document.getElementById('tg-note');
    var note = noteEl ? String(noteEl.value || '').trim() : '';
    if (!note) { window.toast('请填写变更意图', 'err'); return; }
    window.closeModal();
    window.createTask(note.slice(0, 80), typeSel ? typeSel.value : 'feature', 'test', true)
      .then(function (j) {
        if (j) window.toast('已记入需求池 ' + j.id + '；发起交付请切到 DA', 'ok');
      });
  };

  // 角色切换必须同时写服务端会话：只改前端 S.me 会让界面显示「能拍板」，点下去 403。
  window.setRole = async function (id) {
    if (!window.ROLES[id]) return;
    try {
      var j = await api('/api/role', { method: 'POST', body: JSON.stringify({ role: id }) });
      window.S.me = j.role;
      window.toast('已切换为 ' + j.name + ' · 只显示与你有关的待办与确认', 'ok');
    } catch (e) {
      fail(e);
    }
    return refreshAll();
  };

  /* ---------- 停用本地模拟（后端没有对应接口，如实提示） ---------- */

  function notWired(what) {
    return function () {
      window.toast(what + '：后端尚未提供对应接口（见 SPEC §7）', 'warn');
    };
  }

  // 本地七步执行器：接后端后绝不能再跑，否则会和真实投影打架。
  window.launchStep = notWired('本地执行链路');
  window.execStep = function () { return true; };
  window.intervene = notWired('人工介入');
  window.resumeItem = notWired('继续执行');
  window.createFix = notWired('追加子工作项');
  window.splitStory = notWired('拆解 Story');
  window.doneItem = notWired('标记完成');
  window.recompute = notWired('重算制品');
  window.recomputeAll = notWired('批量重算');

  // 制品 / 代码 / 提交 / 打 tag / 部署：都是 mock，一律不写本地状态。
  window.saveDraft = notWired('制品在线编辑');
  window.saveCodeDraft = notWired('代码在线编辑');
  window.genCode = notWired('代码生成');
  window.runTests = notWired('测试执行');
  window.buildImage = notWired('镜像构建');
  window.doCommit = notWired('提交');
  window.doTag = notWired('打 tag');
  window.deployTag = notWired('部署');
  window.saveBrd = notWired('BRD 保存');
  window.brdSuggest = notWired('BRD 续写');

  /* ---------- 本地快照：必须停用 ----------
   * 否则本地快照会掩盖后端真实状态，刷新时还可能把假数据写回去。
   */
  window._snapOn = false;
  window.saveSnap = function () { /* 接后端后不再写本地快照 */ };
  window.loadSnap = function () { return false; };
  try { localStorage.removeItem('i3:snap'); } catch (e) { /* 隐私模式：忽略 */ }

  /* ---------- SSE：只通知「有变化了」 ---------- */

  var stream = null;
  var streamTimer = null;

  function connectStream() {
    try {
      stream = new EventSource(API + '/api/timeline/stream?pbc=' + encodeURIComponent(PBC));
    } catch (e) {
      return;
    }
    stream.addEventListener('change', function () {
      // 一次交互可能连着落好几条事实：合并成一次重拉即可。
      clearTimeout(streamTimer);
      streamTimer = setTimeout(function () { refreshAll(); }, 120);
    });
    stream.onerror = function () {
      // EventSource 自带重连；这里什么都不做，避免自己造出重连风暴。
    };
  }

  /* ---------- 启动 ---------- */

  window.S.pbcId = PBC;
  // 首屏先把原型自带的演示数据清空：宁可短暂空白，也不让人误以为那是账本。
  // 真正的投影由下面的 refreshAll() 从 /api/state + /api/overview 拉取。
  applyToPrototype({ versions: {}, commits: [], deploys: [], annos: [], name: '' },
    { role: 'BA', items: [] });
  origRefreshAll.call(window);

  connectStream();
  refreshAll();

  // 供调试：控制台可查当前投影的拉取状态。
  window.__i3 = { refresh: refreshAll, pbc: PBC };
})();