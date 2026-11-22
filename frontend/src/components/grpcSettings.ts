import { t } from "../i18n";
import { RequestService } from "../mygo";
import { getRequest, updateRequest } from "../state";

/** 词典槽登记：由设置页的分发方（socketComposer.ts）统一在语言切换时重取。 */
export type SettingsTextTrack = (node: HTMLElement, key: string, attribute?: "placeholder") => void;

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  textContent?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className !== undefined) node.className = className;
  if (textContent !== undefined) node.textContent = textContent;
  return node;
}

/**
 * gRPC 的设置页（kind="grpc"，由 socketComposer.ts 的设置分发器挂载）：
 * 传输（明文/TLS、跳过证书校验、ServerName、超时）与描述来源（服务器反射 / 本地
 * proto 文件 + import 目录）。控件值都存在请求态里，重画不丢。
 */
export function mountGrpcSettings(host: HTMLElement, track: SettingsTextTrack): void {
  const label = (key: string): HTMLElement => {
    const node = el("span", "grpc-setting-label");
    track(node, key);
    return node;
  };

  const fieldOf = (labelKey: string, control: HTMLElement): HTMLElement => {
    const field = el("label", "field");
    field.append(label(labelKey), control);
    return field;
  };

  /** 二选一的分段按钮组（复用键值表格的类型切换样式）。 */
  const segmented = (
    labelKey: string,
    options: { value: string; labelKey: string }[],
    current: () => string,
    choose: (value: string) => void,
  ): { node: HTMLElement; sync: () => void } => {
    const group = el("span", "kv-kind");
    group.setAttribute("role", "radiogroup");
    group.setAttribute("aria-label", t(labelKey));
    const buttons: HTMLButtonElement[] = [];
    const sync = (): void => {
      for (const [index, item] of buttons.entries()) {
        const selected = options[index]?.value === current();
        item.classList.toggle("active", selected);
        item.setAttribute("aria-pressed", String(selected));
      }
    };
    for (const option of options) {
      const button = el("button", "kv-kind-option");
      button.type = "button";
      track(button, option.labelKey);
      button.setAttribute("aria-pressed", String(option.value === current()));
      button.classList.toggle("active", option.value === current());
      button.addEventListener("click", () => {
        if (option.value === current()) return;
        choose(option.value);
        sync();
      });
      buttons.push(button);
      group.append(button);
    }
    return { node: group, sync };
  };

  const hintOf = (key: string): HTMLElement => {
    const hint = el("p", "hint");
    track(hint, key);
    return hint;
  };

  const plaintext = (): boolean => getRequest().grpcPlaintext;

  // ---- 传输 ----
  const transport = segmented(
    "grpc.transport",
    [
      { value: "plaintext", labelKey: "grpc.transportPlaintext" },
      { value: "tls", labelKey: "grpc.transportTLS" },
    ],
    () => (plaintext() ? "plaintext" : "tls"),
    (value) => {
      updateRequest((state) => {
        state.grpcPlaintext = value === "plaintext";
      });
      syncControls();
    },
  );

  const skipLabel = el("label", "grpc-check");
  const skipBox = document.createElement("input");
  skipBox.type = "checkbox";
  skipBox.checked = getRequest().grpcTlsSkipVerify;
  const skipText = el("span");
  track(skipText, "grpc.skipVerify");
  skipLabel.append(skipBox, skipText);
  skipBox.addEventListener("change", () => {
    updateRequest((state) => {
      state.grpcTlsSkipVerify = skipBox.checked;
    });
  });

  const serverName = document.createElement("input");
  serverName.type = "text";
  serverName.className = "grpc-server-name";
  serverName.spellcheck = false;
  serverName.autocomplete = "off";
  serverName.value = getRequest().grpcServerName;
  track(serverName, "grpc.serverNamePlaceholder", "placeholder");
  serverName.addEventListener("input", () => {
    updateRequest((state) => {
      state.grpcServerName = serverName.value;
    });
  });

  const timeout = document.createElement("input");
  timeout.type = "number";
  timeout.min = "0";
  timeout.step = "1";
  timeout.className = "grpc-timeout";
  timeout.value = String(getRequest().timeout);
  timeout.addEventListener("input", () => {
    const next = Number(timeout.value);
    updateRequest((state) => {
      state.timeout = Number.isFinite(next) && next > 0 ? Math.floor(next) : 0;
    });
  });

  host.append(
    fieldOf("grpc.transport", transport.node),
    skipLabel,
    fieldOf("grpc.serverNameLabel", serverName),
    fieldOf("grpc.timeoutLabel", timeout),
    hintOf("grpc.timeoutHint"),
  );

  // ---- 描述来源 ----
  const source = segmented(
    "grpc.sourceLabel",
    [
      { value: "reflection", labelKey: "grpc.sourceReflection" },
      { value: "proto", labelKey: "grpc.sourceProto" },
    ],
    () => (getRequest().grpcProtoFiles.length > 0 ? "proto" : "reflection"),
    (value) => {
      updateRequest((state) => {
        if (value === "proto") {
          // proto 模式由「列表非空」表达：还没有文件时先放一行空的等着选。
          if (state.grpcProtoFiles.length === 0) state.grpcProtoFiles = [""];
          return;
        }
        // 回到服务器反射：文件列表清空（import 目录留着，下次再用）。
        state.grpcProtoFiles = [];
      });
      drawFiles();
      drawImports();
      syncControls();
    },
  );

  const filesList = el("div", "grpc-path-list");
  const filesEmpty = el("p", "hint");
  track(filesEmpty, "grpc.protoFilesEmpty");
  const addFile = el("button", "grpc-path-add");
  addFile.type = "button";
  track(addFile, "grpc.addProtoFile");
  const filesError = el("p", "hint grpc-error");
  filesError.hidden = true;

  const importsList = el("div", "grpc-path-list");
  const importsEmpty = el("p", "hint");
  track(importsEmpty, "grpc.importPathsEmpty");
  const addImport = el("button", "grpc-path-add");
  addImport.type = "button";
  track(addImport, "grpc.addImportPath");

  host.append(
    fieldOf("grpc.sourceLabel", source.node),
    label("grpc.protoFilesLabel"),
    filesList,
    filesEmpty,
    addFile,
    filesError,
    label("grpc.importPathsLabel"),
    importsList,
    importsEmpty,
    addImport,
    hintOf("grpc.protoHint"),
  );

  const drawFiles = (): void => {
    const files = getRequest().grpcProtoFiles;
    filesList.textContent = "";
    filesList.hidden = files.length === 0;
    filesEmpty.hidden = files.length > 0;
    files.forEach((path, index) => {
      const row = el("div", "grpc-path-row");
      const value = el("span", "grpc-path-value", path === "" ? t("grpc.noFileChosen") : path);
      if (path === "") value.classList.add("empty");
      value.title = path;
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "kv-delete";
      remove.textContent = "×";
      remove.title = t("common.delete");
      remove.addEventListener("click", () => {
        updateRequest((state) => {
          state.grpcProtoFiles = state.grpcProtoFiles.filter((_, position) => position !== index);
        });
        drawFiles();
        syncControls();
      });
      row.append(value, remove);
      filesList.append(row);
    });
    // 「描述来源」由文件列表是否为空推导：删掉最后一个文件就回到服务器反射。
    source.sync();
  };

  const drawImports = (): void => {
    const paths = getRequest().grpcImportPaths;
    importsList.textContent = "";
    importsList.hidden = paths.length === 0;
    importsEmpty.hidden = paths.length > 0;
    paths.forEach((path, index) => {
      const row = el("div", "grpc-path-row");
      const input = document.createElement("input");
      input.type = "text";
      input.className = "grpc-path-input";
      input.spellcheck = false;
      input.autocomplete = "off";
      input.placeholder = t("grpc.importPathPlaceholder");
      input.value = path;
      input.addEventListener("input", () => {
        // 就地改请求态里的数组项：输入过程中不重画，光标不跳。
        const list = getRequest().grpcImportPaths;
        if (index < list.length) list[index] = input.value;
        updateRequest();
      });
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "kv-delete";
      remove.textContent = "×";
      remove.title = t("common.delete");
      remove.addEventListener("click", () => {
        updateRequest((state) => {
          state.grpcImportPaths = state.grpcImportPaths.filter((_, position) => position !== index);
        });
        drawImports();
      });
      row.append(input, remove);
      importsList.append(row);
    });
  };

  addFile.addEventListener("click", () => {
    void RequestService.pickFile()
      .then((path) => {
        if (path === "") return;
        updateRequest((state) => {
          // 空行是「还没选文件」的占位：真正选到文件时先把它顶掉。
          const kept = state.grpcProtoFiles.filter((item) => item.trim() !== "");
          state.grpcProtoFiles = [...kept, path];
        });
        filesError.hidden = true;
        drawFiles();
        syncControls();
      })
      .catch((error: unknown) => {
        filesError.textContent = t("grpc.filePickFailed", {
          error: error instanceof Error ? error.message : String(error),
        });
        filesError.hidden = false;
      });
  });

  addImport.addEventListener("click", () => {
    updateRequest((state) => {
      state.grpcImportPaths = [...state.grpcImportPaths, ""];
    });
    drawImports();
  });

  // 明文下 TLS 的两个控件没有意义：灰掉，值留着（切回 TLS 就有了）。
  const syncControls = (): void => {
    const tls = !plaintext();
    skipBox.disabled = !tls;
    serverName.disabled = !tls;
    skipLabel.classList.toggle("disabled", !tls);
  };

  drawFiles();
  drawImports();
  syncControls();
}
