'use strict';

const byId = (id) => document.getElementById(id);
const type = byId('inbound-type');
const source = byId('source-no');
const material = byId('material');
const warehouse = byId('warehouse');
const quantity = byId('quantity');
const receiptNo = byId('receipt-no');
const draftKey = `material-inbound:draft:${location.pathname}`;

let options;
let pendingRequest;
let submitting = false;
let ledgerQuery = 0;

async function request(path, init) {
  const response = await fetch(path, { cache: 'no-store', ...init });
  const body = await response.json().catch(() => null);
  if (!response.ok || !body) {
    const error = new Error(body?.error || `服务请求失败（HTTP ${response.status}）`);
    error.status = response.status;
    throw error;
  }
  return { body, status: response.status };
}

function message(id, text = '', tone = 'neutral') {
  const element = byId(id);
  element.dataset.tone = tone;
  element.textContent = text;
  element.hidden = !text;
}

function selectOptions(element, placeholder, items, value = element.value) {
  element.replaceChildren(new Option(placeholder, ''), ...items.map(({ id, name }) => new Option(name, id)));
  if (items.some((item) => item.id === value)) {
    element.value = value;
  }
  element.disabled = items.length === 0;
}

function selectedType() {
  return options?.types.find((entry) => entry.id === type.value);
}

function selectedMaterial() {
  return options?.materials.find((entry) => entry.code === material.value && entry.warehouseId === warehouse.value);
}

function updateSource(value = source.value) {
  const entry = selectedType();
  const sources = entry?.sources ?? [];
  selectOptions(source, '请选择来源单号', sources.map(({ id, name }) => ({ id, name: `${id} · ${name}` })), value);
  byId('source-help').textContent = entry?.sourceLabel || '选择入库类型后可选来源单据';
  byId('source-type').textContent = entry?.sourceLabel || '—';
  byId('asset-owner').textContent = entry?.assetOwner || '—';
  byId('finance-tag').textContent = entry?.financeTag || '—';
}

function formatQuantity(value, unit) {
  return value == null || value === '' ? '—' : `${value} ${unit || ''}`.trim();
}

function updatePlan() {
  const entry = selectedMaterial();
  byId('plan-material').textContent = entry ? `${entry.name} · ${entry.code} · ${entry.warehouseName}` : '选择材料后查看计划余量';
  byId('plan-remaining').textContent = formatQuantity(entry?.remainingQty, entry?.unit);
  byId('plan-quantity').textContent = formatQuantity(quantity.value.trim(), entry?.unit);
  byId('quantity-unit').textContent = entry?.unit || '—';
}

function updateWarehouse(value = warehouse.value) {
  const warehouses = options.materials
    .filter((entry) => entry.code === material.value)
    .map(({ warehouseId, warehouseName }) => ({ id: warehouseId, name: warehouseName }));
  selectOptions(warehouse, '请选择库房', warehouses, value);
  if (!warehouse.value && warehouses.length === 1) {
    warehouse.value = warehouses[0].id;
  }
  updatePlan();
}

function updateControls() {
  byId('receiving-fields').disabled = submitting || !options;
  byId('submit-inbound').disabled = submitting || !options;
  byId('save-draft').disabled = submitting || !options;
}

async function loadOptions(draft) {
  byId('reload-options').hidden = true;
  message('options-status', '正在读取入库选项…');
  try {
    ({ body: options } = await request('./api/options'));
    const environment = byId('environment');
    const environmentLabels = { test: '测试环境 (TEST)', sit: '集成测试环境 (SIT)' };
    environment.textContent = environmentLabels[options.environment] || options.environment || '';
    environment.hidden = !environment.textContent;
    byId('receipt-count').textContent = `已确认入库单数 ${options.receiptCount}`;

    const types = options.types.map(({ id, name }) => ({ id, name: `${name} (${id})` }));
    const materials = new Map(options.materials.map(({ code, name }) => [code, { id: code, name: `${name} (${code})` }]));
    selectOptions(type, '请选择入库类型', types, draft?.type ?? type.value);
    selectOptions(material, '请选择材料', [...materials.values()], draft?.materialCode ?? material.value);
    if (typeof draft?.quantity === 'string') {
      quantity.value = draft.quantity;
    }
    updateSource(draft?.sourceNo);
    updateWarehouse(draft?.warehouseId);
    message('options-status');
    if (draft) {
      message('form-status', '已恢复本机草稿');
    }
  } catch (error) {
    message('options-status', `读取入库选项失败。${error.message}`, 'error');
    byId('reload-options').hidden = false;
  } finally {
    updateControls();
  }
}

function formValues() {
  return {
    type: type.value,
    sourceNo: source.value,
    materialCode: material.value,
    warehouseId: warehouse.value,
    quantity: quantity.value.trim(),
    unit: selectedMaterial()?.unit ?? '',
  };
}

function readDraft() {
  try {
    return JSON.parse(localStorage.getItem(draftKey));
  } catch {
    return null;
  }
}

byId('save-draft').addEventListener('click', () => {
  try {
    localStorage.setItem(draftKey, JSON.stringify(formValues()));
    message('form-status', '草稿已保存到当前浏览器');
  } catch {
    message('form-status', '浏览器无法保存本机草稿。表单输入仍保留。', 'error');
  }
});

function requestIdFor(payload) {
  const key = JSON.stringify(payload);
  if (pendingRequest?.key !== key) {
    // 测试部署可使用 HTTP，getRandomValues 在此也能生成重试所需的请求号。
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    const id = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
    pendingRequest = { key, id };
  }
  return pendingRequest.id;
}

function receiptRow(receipt) {
  const row = document.createElement('tr');
  const heading = document.createElement('th');
  heading.scope = 'row';
  const number = document.createElement('button');
  number.type = 'button';
  number.className = 'receipt-link';
  number.textContent = receipt.receiptNo;
  number.addEventListener('click', () => {
    receiptNo.value = receipt.receiptNo;
    receiptNo.focus();
    void loadLedger(receipt.receiptNo);
  });
  heading.append(number);
  row.append(heading);
  const typeName = options?.types.find((entry) => entry.id === receipt.type)?.name ?? receipt.type;
  const cells = [
    [typeName, receipt.type === typeName ? '' : receipt.type],
    [receipt.sourceNo, receipt.sourceLabel],
    [receipt.materialName, receipt.materialCode],
    [receipt.warehouseName, receipt.warehouseId],
    [formatQuantity(receipt.quantity, receipt.unit), '', 'numeric'],
    [receipt.assetOwner],
    [receipt.financeTag],
  ];
  for (const [value, detail, className] of cells) {
    const cell = row.insertCell();
    cell.textContent = value;
    if (className) {
      cell.className = className;
    }
    if (detail) {
      const secondary = document.createElement('small');
      secondary.textContent = detail;
      cell.append(secondary);
    }
  }
  return row;
}

async function loadLedger(number = '') {
  const query = ++ledgerQuery;
  const table = byId('ledger-table');
  const queryButton = byId('query-ledger');
  table.hidden = true;
  queryButton.disabled = true;
  message('ledger-status', '正在读取台账…');
  try {
    const path = number ? `./api/inbounds/${encodeURIComponent(number)}` : './api/inbounds';
    const { body } = await request(path);
    if (query !== ledgerQuery) return;
    const receipts = number ? [body] : body.receipts;
    byId('receipts').replaceChildren(...receipts.map(receiptRow));
    table.hidden = receipts.length === 0;
    if (!number) {
      byId('receipt-count').textContent = `已确认入库单数 ${receipts.length}`;
    }
    const result = number ? `已找到入库单 ${number}` : '暂无已确认入库单';
    message('ledger-status', number || receipts.length === 0 ? result : '');
  } catch (error) {
    if (query !== ledgerQuery) return;
    if (number && error.status === 404) {
      message('ledger-status', `未找到入库单 ${number}。请核对单号后重新查询。`);
    } else {
      message('ledger-status', `查询台账失败。${error.message}`, 'error');
    }
  } finally {
    if (query === ledgerQuery) {
      queryButton.disabled = false;
    }
  }
}

byId('receiving-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  if (submitting || !options) return;
  const payload = formValues();
  submitting = true;
  updateControls();
  message('form-status', '正在确认入库…');
  try {
    const { body: receipt } = await request('./api/inbounds', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ requestId: requestIdFor(payload), ...payload }),
    });
    pendingRequest = undefined;
    quantity.value = '';
    receiptNo.value = receipt.receiptNo;
    message('form-status', `入库成功，单号 ${receipt.receiptNo}。计划剩余 ${formatQuantity(receipt.remainingQty, receipt.unit)}。`, 'success');
    // 确认后清理已提交的草稿，浏览器存储失败不影响已确认的入库单。
    try {
      localStorage.removeItem(draftKey);
    } catch {
      // 本机存储不可用时仍然读取服务的确认结果。
    }
    await loadOptions();
    await loadLedger();
  } catch (error) {
    message('form-status', error.message, 'error');
    if (error.status === 409) {
      await loadOptions();
    }
  } finally {
    submitting = false;
    updateControls();
  }
});

byId('ledger-form').addEventListener('submit', (event) => {
  event.preventDefault();
  void loadLedger(receiptNo.value.trim());
});

byId('check-health').addEventListener('click', async (event) => {
  const button = event.currentTarget;
  button.disabled = true;
  message('health-status', '正在检查部署服务…');
  try {
    const { body, status } = await request('./healthz');
    const result = body.ok === true ? '服务正常' : '服务异常';
    message('health-status', `${result}（HTTP ${status}） · 单元 ${body.unit} · 版本 ${body.commit} · 环境 ${body.environment}`, body.ok === true ? 'success' : 'error');
  } catch (error) {
    message('health-status', `健康检查失败。${error.message}`, 'error');
  } finally {
    button.disabled = false;
  }
});

byId('reload-options').addEventListener('click', async () => {
  await loadOptions();
  await loadLedger();
});

type.addEventListener('change', () => updateSource(''));
material.addEventListener('change', () => updateWarehouse(''));
warehouse.addEventListener('change', updatePlan);
quantity.addEventListener('input', updatePlan);
byId('receiving-form').addEventListener('input', () => message('form-status'));

void (async () => {
  await loadOptions(readDraft());
  await loadLedger();
})();
