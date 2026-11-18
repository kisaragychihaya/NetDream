import { AUTH_TYPES, getRequest, subscribe, updateRequest, type AuthType } from "../state";

const AUTH_LABELS: Record<AuthType, string> = {
  none: "无认证",
  basic: "Basic Auth",
  bearer: "Bearer Token",
};

export function mountAuthEditor(host: HTMLElement): () => void {
  host.textContent = "";

  const types = document.createElement("div");
  types.className = "auth-types";
  types.setAttribute("role", "radiogroup");
  const radios: HTMLInputElement[] = [];

  for (const type of AUTH_TYPES) {
    const label = document.createElement("label");
    label.className = "auth-type";
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "auth-type";
    radio.value = type;
    radio.addEventListener("change", () => {
      if (!radio.checked) return;
      updateRequest((state) => {
        state.auth.type = type;
      });
    });
    const text = document.createElement("span");
    text.textContent = AUTH_LABELS[type];
    label.append(radio, text);
    types.append(label);
    radios.push(radio);
  }

  const panel = document.createElement("div");
  panel.className = "auth-panel";
  host.append(types, panel);

  const createField = (
    labelText: string,
    inputType: "text" | "password",
    value: string,
    placeholder: string,
    set: (next: string) => void,
  ): { field: HTMLElement; input: HTMLInputElement } => {
    const field = document.createElement("label");
    field.className = "field";
    const label = document.createElement("span");
    label.textContent = labelText;
    const input = document.createElement("input");
    input.type = inputType;
    input.spellcheck = false;
    input.autocomplete = "off";
    input.placeholder = placeholder;
    input.value = value;
    input.addEventListener("input", () => {
      set(input.value);
      updateRequest();
    });
    field.append(label, input);
    return { field, input };
  };

  let renderedType: AuthType | null = null;
  let syncValues: (() => void) | null = null;

  const renderPanel = (): void => {
    const state = getRequest();
    renderedType = state.auth.type;
    syncValues = null;
    panel.textContent = "";

    switch (state.auth.type) {
      case "none": {
        const hint = document.createElement("p");
        hint.className = "hint";
        hint.textContent = "该请求不携带认证信息。";
        panel.append(hint);
        break;
      }
      case "basic": {
        const fields = document.createElement("div");
        fields.className = "auth-fields";
        const username = createField("用户名", "text", state.auth.username, "用户名", (next) => {
          getRequest().auth.username = next;
        });
        const password = createField("密码", "password", state.auth.password, "密码", (next) => {
          getRequest().auth.password = next;
        });
        fields.append(username.field, password.field);
        syncValues = () => {
          const auth = getRequest().auth;
          if (username.input.value !== auth.username) username.input.value = auth.username;
          if (password.input.value !== auth.password) password.input.value = auth.password;
        };
        const hint = document.createElement("p");
        hint.className = "hint";
        hint.textContent = "发送时添加 Authorization: Basic 头（用户名与密码的 base64）。";
        panel.append(fields, hint);
        break;
      }
      case "bearer": {
        const fields = document.createElement("div");
        fields.className = "auth-fields";
        const token = createField("Token", "text", state.auth.token, "粘贴 Bearer Token", (next) => {
          getRequest().auth.token = next;
        });
        fields.append(token.field);
        syncValues = () => {
          const auth = getRequest().auth;
          if (token.input.value !== auth.token) token.input.value = auth.token;
        };
        const hint = document.createElement("p");
        hint.className = "hint";
        hint.textContent = "发送时添加 Authorization: Bearer <token> 头。";
        panel.append(fields, hint);
        break;
      }
    }
  };

  const sync = (): void => {
    const state = getRequest();
    for (const radio of radios) radio.checked = radio.value === state.auth.type;
    if (state.auth.type !== renderedType) renderPanel();
    else syncValues?.();
  };

  sync();
  const unsubscribe = subscribe(sync);

  return () => {
    unsubscribe();
    host.textContent = "";
  };
}
