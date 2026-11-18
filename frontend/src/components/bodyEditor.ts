import {
  BODY_TYPES,
  emptyFormRow,
  emptyRow,
  getRequest,
  RAW_TYPES,
  setFormFilePath,
  subscribe,
  updateRequest,
  updateResponse,
  type BodyType,
  type RawType,
} from "../state";
import { RequestService } from "../mygo";
import { mountKVTable } from "./kvTable";

const BODY_LABELS: Record<BodyType, string> = {
  none: "none",
  formData: "form-data",
  urlencoded: "urlencoded",
  binary: "binary",
  raw: "raw",
};

const RAW_LABELS: Record<RawType, string> = {
  json: "JSON",
  text: "Text",
  xml: "XML",
  html: "HTML",
};

const RAW_PLACEHOLDERS: Record<RawType, string> = {
  json: '{\n  "hello": "world"\n}',
  text: "纯文本请求体",
  xml: "<note>\n  <to>NetDream</to>\n</note>",
  html: "<h1>Hello</h1>",
};

// form-data 文件字段的「选择」与 binary 共用同一个原生对话框：拿到非空路径就
// 写回对应行；取消（空串）什么都不做。拖入的路径由 main.ts 的 onFileDrop 走同一条写回。
async function pickFormFile(index: number): Promise<void> {
  try {
    const picked = await RequestService.pickFile();
    if (picked !== "") setFormFilePath(index, picked);
  } catch (error) {
    updateResponse({ error: error instanceof Error ? error.message : String(error) });
  }
}

export function mountBodyEditor(host: HTMLElement): () => void {
  host.textContent = "";

  const types = document.createElement("div");
  types.className = "body-types";
  types.setAttribute("role", "radiogroup");
  const radios: HTMLInputElement[] = [];

  for (const type of BODY_TYPES) {
    const label = document.createElement("label");
    label.className = "body-type";
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "body-type";
    radio.value = type;
    radio.addEventListener("change", () => {
      if (!radio.checked) return;
      updateRequest((state) => {
        state.bodyType = type;
      });
    });
    const text = document.createElement("span");
    text.textContent = BODY_LABELS[type];
    label.append(radio, text);
    types.append(label);
    radios.push(radio);
  }

  const panel = document.createElement("div");
  panel.className = "body-panel";
  host.append(types, panel);

  let renderedType: BodyType | null = null;
  let syncRaw: (() => void) | null = null;
  let syncBinary: (() => void) | null = null;
  let disposeChild: (() => void) | null = null;

  const renderPanel = (): void => {
    const state = getRequest();
    renderedType = state.bodyType;
    syncRaw = null;
    syncBinary = null;
    disposeChild?.();
    disposeChild = null;
    panel.textContent = "";

    switch (state.bodyType) {
      case "none": {
        const hint = document.createElement("p");
        hint.className = "hint";
        hint.textContent = "该请求不包含请求体。";
        panel.append(hint);
        break;
      }
      case "formData": {
        disposeChild = mountKVTable(panel, {
          rows: () => getRequest().formData,
          append: () => {
            updateRequest((value) => {
              value.formData = [...value.formData, emptyFormRow()];
            });
          },
          remove: (index) => {
            updateRequest((value) => {
              value.formData = value.formData.filter((_, position) => position !== index);
            });
          },
          addLabel: "+ 添加字段",
          withContentType: true,
          withFileKind: true,
          pickFile: (index) => {
            void pickFormFile(index);
          },
          hint: "以 multipart/form-data 发送；「内容类型」留空时使用默认值；文件字段发送时读取所选文件。",
        });
        break;
      }
      case "urlencoded": {
        disposeChild = mountKVTable(panel, {
          rows: () => getRequest().urlEncoded,
          append: () => {
            updateRequest((value) => {
              value.urlEncoded = [...value.urlEncoded, emptyRow()];
            });
          },
          remove: (index) => {
            updateRequest((value) => {
              value.urlEncoded = value.urlEncoded.filter((_, position) => position !== index);
            });
          },
          addLabel: "+ 添加字段",
          hint: "以 application/x-www-form-urlencoded 发送。",
        });
        break;
      }
      case "raw": {
        const bar = document.createElement("div");
        bar.className = "raw-bar";

        const select = document.createElement("select");
        select.setAttribute("aria-label", "请求体类型");
        for (const rawType of RAW_TYPES) {
          const option = document.createElement("option");
          option.value = rawType;
          option.textContent = RAW_LABELS[rawType];
          select.append(option);
        }
        select.value = state.rawType;

        const area = document.createElement("textarea");
        area.className = "raw-area";
        area.spellcheck = false;
        area.value = state.raw;
        area.placeholder = RAW_PLACEHOLDERS[state.rawType];
        area.addEventListener("input", () => {
          updateRequest((value) => {
            value.raw = area.value;
          });
        });

        select.addEventListener("change", () => {
          const next = RAW_TYPES.find((candidate) => candidate === select.value);
          if (!next) return;
          updateRequest((value) => {
            value.rawType = next;
          });
        });

        syncRaw = () => {
          const current = getRequest();
          if (select.value !== current.rawType) select.value = current.rawType;
          if (area.value !== current.raw) area.value = current.raw;
          area.placeholder = RAW_PLACEHOLDERS[current.rawType];
        };

        bar.append(select);
        panel.append(bar, area);
        break;
      }
      case "binary": {
        const box = document.createElement("div");
        box.className = "binary-editor";

        const pick = document.createElement("button");
        pick.type = "button";
        pick.textContent = "选择文件";

        const path = document.createElement("span");
        path.className = "binary-path";

        const error = document.createElement("p");
        error.className = "binary-error";
        error.hidden = true;

        syncBinary = () => {
          const current = getRequest().binaryPath;
          path.textContent = current || "未选择文件";
          path.classList.toggle("empty", current === "");
          path.title = current;
        };
        syncBinary();

        pick.addEventListener("click", async () => {
          error.hidden = true;
          pick.disabled = true;
          try {
            const picked = await RequestService.pickFile();
            if (picked !== "") {
              updateRequest((value) => {
                value.binaryPath = picked;
              });
            }
          } catch (err) {
            if (!pick.isConnected) return;
            error.textContent = err instanceof Error ? err.message : String(err);
            error.hidden = false;
          } finally {
            pick.disabled = false;
          }
        });

        box.append(pick, path);

        const note = document.createElement("p");
        note.className = "hint";
        note.textContent = "读取该文件作为请求体（最大 100MB）；未显式设置 Content-Type 时使用 application/octet-stream。";

        panel.append(box, error, note);
        break;
      }
    }
  };

  const sync = (): void => {
    const state = getRequest();
    for (const radio of radios) radio.checked = radio.value === state.bodyType;
    if (state.bodyType !== renderedType) renderPanel();
    else {
      syncRaw?.();
      syncBinary?.();
    }
  };

  sync();
  const unsubscribe = subscribe(sync);

  return () => {
    unsubscribe();
    disposeChild?.();
    disposeChild = null;
    host.textContent = "";
  };
}
