import type { APIResponse } from "./mygo";

export type AssertionType = "statusEquals" | "bodyContains" | "bodyNotContains" | "responseTimeLt";

export interface AssertionRule {
  id: string;
  enabled: boolean;
  type: AssertionType;
  value: string;
}

export interface AssertionResult {
  rule: AssertionRule;
  pass: boolean;
  actual: string;
}

export interface AssertionTypeOption {
  type: AssertionType;
  label: string;
  placeholder: string;
}

export const ASSERTION_TYPES: readonly AssertionTypeOption[] = [
  { type: "statusEquals", label: "状态码等于", placeholder: "200" },
  { type: "bodyContains", label: "响应体包含", placeholder: "关键词" },
  { type: "bodyNotContains", label: "响应体不包含", placeholder: "关键词" },
  { type: "responseTimeLt", label: "响应时间小于(ms)", placeholder: "1000" },
];

export const ASSERTION_TYPE_IDS: readonly AssertionType[] = ASSERTION_TYPES.map((option) => option.type);

export function assertionTypeOption(type: AssertionType): AssertionTypeOption {
  for (const option of ASSERTION_TYPES) {
    if (option.type === type) return option;
  }
  return { type, label: type, placeholder: "" };
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
        return { rule, pass: false, actual: `期望值格式错误：${rule.value}` };
      }
      return {
        rule,
        pass: res.status === expected,
        actual: `实际状态码 ${res.status}`,
      };
    }
    case "bodyContains": {
      const found = res.body.includes(rule.value);
      return {
        rule,
        pass: found,
        actual: found ? `响应体包含「${rule.value}」` : `响应体不包含「${rule.value}」`,
      };
    }
    case "bodyNotContains": {
      const found = res.body.includes(rule.value);
      return {
        rule,
        pass: !found,
        actual: found ? `响应体包含「${rule.value}」` : `响应体不包含「${rule.value}」`,
      };
    }
    case "responseTimeLt": {
      const expected = Number(rule.value);
      if (!Number.isFinite(expected)) {
        return { rule, pass: false, actual: `期望值格式错误：${rule.value}` };
      }
      return {
        rule,
        pass: res.timeMS < expected,
        actual: `实际耗时 ${res.timeMS} ms`,
      };
    }
    default:
      return { rule, pass: false, actual: "未知的断言类型" };
  }
}
