import {
  ASSERTION_TYPES,
  assertionTypeOption,
  createAssertionRule,
  type AssertionResult,
  type AssertionRule,
} from "../assert";
import type { ScriptTest } from "../mygo";
import { getRequest, getResponse, subscribe, updateRequest, updateResponse } from "../state";

export function mountAssertionsPanel(host: HTMLElement): () => void {
  host.textContent = "";
  let collapsed = false;
  let renderedRules: readonly AssertionRule[] | null = null;

  const header = document.createElement("button");
  header.type = "button";
  header.className = "assert-header";
  header.setAttribute("aria-expanded", "true");
  const title = document.createElement("span");
  title.textContent = "断言与校验";
  const chevron = document.createElement("span");
  chevron.className = "chevron";
  chevron.textContent = "▾";
  header.append(title, chevron);
  header.addEventListener("click", () => {
    collapsed = !collapsed;
    host.classList.toggle("collapsed", collapsed);
    header.setAttribute("aria-expanded", collapsed ? "false" : "true");
  });

  const body = document.createElement("div");
  body.className = "assert-body";

  const rulesHost = document.createElement("div");
  rulesHost.className = "assert-rules";

  const addButton = document.createElement("button");
  addButton.type = "button";
  addButton.className = "add-row";
  addButton.textContent = "+ 添加断言";

  const testsHost = document.createElement("div");
  testsHost.className = "script-tests";
  testsHost.hidden = true;

  const summary = document.createElement("div");
  summary.className = "assert-summary";

  body.append(rulesHost, addButton, testsHost, summary);
  host.append(header, body);

  const commitRules = (): void => {
    updateRequest();
    if (getResponse().results.length > 0) updateResponse({ results: [] });
  };

  const ruleRow = (rule: AssertionRule): HTMLElement => {
    const row = document.createElement("div");
    row.className = "assert-rule";
    row.dataset.ruleId = rule.id;

    const top = document.createElement("div");
    top.className = "assert-rule-top";

    const enabled = document.createElement("input");
    enabled.type = "checkbox";
    enabled.checked = rule.enabled;
    enabled.title = "启用";
    enabled.addEventListener("change", () => {
      rule.enabled = enabled.checked;
      commitRules();
    });

    const select = document.createElement("select");
    select.setAttribute("aria-label", "断言类型");
    for (const option of ASSERTION_TYPES) {
      const item = document.createElement("option");
      item.value = option.type;
      item.textContent = option.label;
      select.append(item);
    }
    select.value = rule.type;

    const value = document.createElement("input");
    value.type = "text";
    value.className = "assert-value";
    value.spellcheck = false;
    value.autocomplete = "off";
    value.placeholder = assertionTypeOption(rule.type).placeholder;
    value.value = rule.value;
    value.addEventListener("input", () => {
      rule.value = value.value;
      commitRules();
    });

    select.addEventListener("change", () => {
      const next = ASSERTION_TYPES.find((option) => option.type === select.value);
      if (!next) return;
      rule.type = next.type;
      value.placeholder = next.placeholder;
      commitRules();
    });

    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "icon-btn";
    remove.title = "删除";
    remove.textContent = "×";
    remove.addEventListener("click", () => {
      updateRequest((state) => {
        state.assertions = state.assertions.filter((candidate) => candidate !== rule);
      });
      updateResponse({ results: [] });
      renderRules();
      renderResults();
    });

    top.append(enabled, select, remove);

    const result = document.createElement("div");
    result.className = "assert-result";

    row.append(top, value, result);
    return row;
  };

  function renderRules(): void {
    rulesHost.textContent = "";
    const rules = getRequest().assertions;
    renderedRules = rules;
    if (rules.length === 0) {
      const empty = document.createElement("p");
      empty.className = "assert-empty";
      empty.textContent = "还没有断言，点击下方按钮添加。";
      rulesHost.append(empty);
      return;
    }
    for (const rule of rules) rulesHost.append(ruleRow(rule));
  }

  function renderResults(): void {
    const { results, response, error, sending } = getResponse();
    const rules = getRequest().assertions;

    const byId = new Map<string, AssertionResult>();
    for (const result of results) byId.set(result.rule.id, result);
    const enabledById = new Map<string, boolean>();
    for (const rule of rules) enabledById.set(rule.id, rule.enabled);

    for (const row of rulesHost.querySelectorAll<HTMLElement>(".assert-rule")) {
      const id = row.dataset.ruleId;
      const slot = row.querySelector<HTMLElement>(".assert-result");
      if (id === undefined || slot === null) continue;

      slot.className = "assert-result";
      slot.textContent = "";

      const result = byId.get(id);
      if (result) {
        slot.classList.add(result.pass ? "pass" : "fail");
        const mark = document.createElement("span");
        mark.className = "assert-mark";
        mark.textContent = result.pass ? "✓" : "✗";
        const actual = document.createElement("span");
        actual.className = "assert-actual";
        actual.textContent = result.actual;
        slot.append(mark, actual);
      } else if (enabledById.get(id) === false) {
        slot.classList.add("muted");
        slot.textContent = "已禁用";
      }
    }

    if (results.length > 0) {
      const passed = results.filter((result) => result.pass).length;
      summary.textContent = `通过 ${passed} / ${results.length}`;
      summary.className = passed === results.length ? "assert-summary pass" : "assert-summary fail";
      return;
    }

    summary.className = "assert-summary";
    if (sending) {
      summary.textContent = "等待响应…";
    } else if (error) {
      summary.textContent = "请求失败，断言未执行";
    } else if (response) {
      summary.textContent = rules.some((rule) => rule.enabled) ? "断言未执行" : "没有启用的断言";
    } else {
      summary.textContent = "发送请求后显示结果";
    }
  }

  function renderScriptTests(): void {
    const tests = getResponse().scriptTests;
    testsHost.textContent = "";
    testsHost.hidden = tests.length === 0;
    if (tests.length === 0) return;

    const title = document.createElement("div");
    title.className = "script-tests-title";
    const passed = tests.filter((test) => test.pass).length;
    title.textContent = `脚本测试 ${passed} / ${tests.length}`;
    testsHost.append(title);

    for (const test of tests) testsHost.append(scriptTestRow(test));
  }

  function scriptTestRow(test: ScriptTest): HTMLElement {
    const row = document.createElement("div");
    row.className = test.pass ? "script-test pass" : "script-test fail";

    const head = document.createElement("div");
    head.className = "script-test-head";

    const mark = document.createElement("span");
    mark.className = "assert-mark";
    mark.textContent = test.pass ? "✓" : "✗";

    const name = document.createElement("span");
    name.className = "script-test-name";
    name.textContent = test.name;

    head.append(mark, name);
    row.append(head);

    if (!test.pass && test.error !== "") {
      const error = document.createElement("div");
      error.className = "script-test-error";
      error.textContent = test.error;
      row.append(error);
    }

    return row;
  }

  addButton.addEventListener("click", () => {
    const rule = createAssertionRule();
    updateRequest((state) => {
      state.assertions = [...state.assertions, rule];
    });
    updateResponse({ results: [] });
    renderRules();
    renderResults();
    for (const row of rulesHost.querySelectorAll<HTMLElement>(".assert-rule")) {
      if (row.dataset.ruleId !== rule.id) continue;
      row.querySelector<HTMLInputElement>(".assert-value")?.focus();
    }
  });

  renderRules();
  renderResults();
  renderScriptTests();

  const unsubscribe = subscribe(() => {
    // loadEntry replaces the rules array: the loaded request's rules must show.
    if (getRequest().assertions !== renderedRules) renderRules();
    renderResults();
    renderScriptTests();
  });

  return () => {
    unsubscribe();
    host.classList.remove("collapsed");
    host.textContent = "";
  };
}
