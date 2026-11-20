import {
  ASSERTION_TYPES,
  assertionLabel,
  assertionPlaceholder,
  createAssertionRule,
  type AssertionResult,
  type AssertionRule,
} from "../assert";
import { onLangChange, t } from "../i18n";
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
  title.textContent = t("assert.title");
  title.dataset.i18n = "assert.title";
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
  addButton.textContent = t("assert.add");

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
    enabled.title = t("common.enabled");
    enabled.addEventListener("change", () => {
      rule.enabled = enabled.checked;
      commitRules();
    });

    const select = document.createElement("select");
    select.setAttribute("aria-label", t("assert.type"));
    for (const option of ASSERTION_TYPES) {
      const item = document.createElement("option");
      item.value = option;
      item.textContent = assertionLabel(option);
      select.append(item);
    }
    select.value = rule.type;

    const value = document.createElement("input");
    value.type = "text";
    value.className = "assert-value";
    value.spellcheck = false;
    value.autocomplete = "off";
    value.placeholder = assertionPlaceholder(rule.type);
    value.value = rule.value;
    value.addEventListener("input", () => {
      rule.value = value.value;
      commitRules();
    });

    select.addEventListener("change", () => {
      const next = ASSERTION_TYPES.find((option) => option === select.value);
      if (!next) return;
      rule.type = next;
      value.placeholder = assertionPlaceholder(next);
      commitRules();
    });

    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "icon-btn";
    remove.title = t("common.delete");
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
      empty.textContent = t("assert.empty");
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
        actual.textContent = t(result.actual.key, result.actual.vars);
        slot.append(mark, actual);
      } else if (enabledById.get(id) === false) {
        slot.classList.add("muted");
        slot.textContent = t("common.disabled");
      }
    }

    if (results.length > 0) {
      const passed = results.filter((result) => result.pass).length;
      summary.textContent = t("assert.summary", { passed, total: results.length });
      summary.className = passed === results.length ? "assert-summary pass" : "assert-summary fail";
      return;
    }

    summary.className = "assert-summary";
    if (sending) {
      summary.textContent = t("assert.waiting");
    } else if (error) {
      summary.textContent = t("assert.requestFailed");
    } else if (response) {
      summary.textContent = rules.some((rule) => rule.enabled) ? t("assert.notRun") : t("assert.noEnabled");
    } else {
      summary.textContent = t("assert.emptySummary");
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
    title.textContent = t("assert.scriptTests", { passed, total: tests.length });
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
  // 语言切换：规则行、结果与脚本测试都带词典文案，整体重算。
  const unsubscribeLang = onLangChange(() => {
    addButton.textContent = t("assert.add");
    renderRules();
    renderResults();
    renderScriptTests();
  });

  return () => {
    unsubscribe();
    unsubscribeLang();
    host.classList.remove("collapsed");
    host.textContent = "";
  };
}
