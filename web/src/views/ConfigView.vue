<script setup>
import PageHeader from '../components/PageHeader.vue';
import { S } from '../store.js';
import { icon } from '../icons.js';

const can = (a) => (S.acts || []).includes(a);
const CONNECTORS = [
  { n: '金蝶财务系统', t: 'ERP', api: '/kingdee/v2/voucher', res: '凭证 / 科目 / 对账', st: '已发现' },
  { n: '企业微信', t: 'IM', api: '/wecom/message/send', res: '消息 / 通讯录', st: '已授权' },
  { n: '制品仓库', t: 'DevOps', api: '/harbor/api/v2.0', res: '镜像 / tag', st: '已连接' },
];
const LANGS = ['Go（当前）', 'Python', 'Java', 'TypeScript'];
</script>

<template>
  <PageHeader
    title="配置 · 企业适配底座"
    sub="SA 的职责：多语言运行时、外部系统连接器、架构与资源管理。"
  >
    <button class="btn small" :disabled="!can('config')"><span v-html="icon('plus', 12)" /> 登记连接器</button>
  </PageHeader>

  <div v-if="!can('config')" class="perm-note">
    <span v-html="icon('lock', 12)" /> 配置是 <b>SA</b> 的职责（其他角色只读）
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('box')" /><h3>连接器 · 系统接口与资源</h3><span class="spacer" /><span class="small muted">资产发现结果（演示数据，未接真实发现）</span></div>
    <table class="tb">
      <thead><tr><th style="width: 140px">系统</th><th style="width: 70px">类型</th><th>接口</th><th style="width: 170px">资源目录</th><th style="width: 80px">状态</th></tr></thead>
      <tbody>
        <tr v-for="c in CONNECTORS" :key="c.n">
          <td><b style="font-weight: 550">{{ c.n }}</b></td>
          <td class="small muted">{{ c.t }}</td>
          <td class="mono small" style="color: var(--info)">{{ c.api }}</td>
          <td class="small muted">{{ c.res }}</td>
          <td><span class="tag t-ok">{{ c.st }}</span></td>
        </tr>
      </tbody>
    </table>
  </div>

  <div class="card">
    <div class="ch"><span v-html="icon('cpu')" /><h3>多语言运行时</h3></div>
    <div class="row wrap">
      <span v-for="(l, i) in LANGS" :key="l" class="tag" :class="i === 0 ? 't-ok' : 't-gray'" style="padding: 5px 9px">{{ l }}{{ i === 0 ? ' · 启用' : ' · 待补模板' }}</span>
    </div>
    <div class="small muted" style="margin-top: 8px">ADR：4 人小组保持单体，多语言是模板维度不是服务拆分依据。</div>
  </div>
</template>
