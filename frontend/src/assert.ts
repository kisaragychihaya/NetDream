import { t } from "./i18n";
import type { APIResponse } from "./mygo";

export type AssertionType = "statusEquals" | "bodyContains" | "bodyNotContains" | "responseTimeLt";

export interface AssertionRule {
  id: string;
  enabled: boolean;
  type: AssertionType;
  value: string;
}

/** 结果文案的词典键与插值变量：存结构而不是成品文案，切语言时按当前语言现取。 */
export interface AssertionActual {
  key: string;
  vars?: Record<string, string | number>;
}

export interface AssertionResult {
  rule: AssertionRule;
  pass: boolean;
  actual: AssertionActual;
}

export const ASSERTION_TYPES: readonly AssertionType[] = [
  "statusEquals",
  "bodyContains",
  "bodyNotContains",
  "responseTimeLt",
];

export const ASSERTION_TYPE_IDS: readonly AssertionType[] = ASSERTION_TYPES;

const ASSERTION_LABEL_KEYS: Record<AssertionType, string> = {
  statusEquals: "assert.statusEquals",
  bodyContains: "assert.bodyContains",
  bodyNotContains: "assert.bodyNotContains",
  responseTimeLt: "assert.responseTimeLt",
};

export function assertionLabel(type: AssertionType): string {
  return t(ASSERTION_LABEL_KEYS[type]);
}

export function assertionPlaceholder(type: AssertionType): string {
  switch (type) {
    case "statusEquals":
      return "200";
    case "bodyContains":
    case "bodyNotContains":
      return t("assert.keywordPlaceholder");
    default:
      return "1000";
  }
}

let idSequence = 0;

export function newRuleId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  idSequence += 1;
  return `rule-${Date.now().toString(36)}-${idSequence}`;
}

export function createAssertionRule(type: AssertionType = "statusEquals"): AssertionRule {
  return { id: newRuleId(), enabled: true, type, value: "" };
}

export function runAssertions(rules: AssertionRule[], res: APIResponse): AssertionResult[] {
  const results: AssertionResult[] = [];
  for (const rule of rules) {
    if (!rule.enabled) continue;
    results.push(runRule(rule, res));
  }
  return results;
}

function runRule(rule: AssertionRule, res: APIResponse): AssertionResult {
  switch (rule.type) {
    case "statusEquals": {
      const expected = Number(rule.value);
      if (!Number.isFinite(expected)) {
        return { rule, pass: false, actual: { key: "assert.actualBadExpected", vars: { value: rule.value } } };
      }
      return {
        rule,
        pass: res.status === expected,
        actual: { key: "assert.actualStatus", vars: { status: res.status } },
      };
    }
    case "bodyContains": {
      const found = res.body.includes(rule.value);
      return {
        rule,
        pass: found,
        actual: {
          key: found ? "assert.actualContains" : "assert.actualNotContains",
          vars: { value: rule.value },
        },
      };
    }
    case "bodyNotContains": {
      const found = res.body.includes(rule.value);
      return {
        rule,
        pass: !found,
        actual: {
          key: found ? "assert.actualContains" : "assert.actualNotContains",
          vars: { value: rule.value },
        },
      };
    }
    case "responseTimeLt": {
      const expected = Number(rule.value);
      if (!Number.isFinite(expected)) {
        return { rule, pass: false, actual: { key: "assert.actualBadExpected", vars: { value: rule.value } } };
      }
      return {
        rule,
        pass: res.timeMS < expected,
        actual: { key: "assert.actualTime", vars: { ms: res.timeMS } },
      };
    }
    default:
      return { rule, pass: false, actual: { key: "assert.actualUnknown" } };
  }
}
