import { onLangChange, t } from "../i18n";
import type { GrpcMethodInfo } from "../mygo";
import { GrpcService } from "../mygo";
import {
  getGrpcMethods,
  grpcListParams,
  grpcTargetProblem,
  setGrpcMethods,
  streamShapeOf,
  subscribeGrpc,
  type GrpcStreamShape,
} from "../grpc";
import { getRequest, subscribe, updateRequest } from "../state";

/** 流式形态的徽标文案与配色类（四色区分）。 */
const SHAPE_LABELS: Record<GrpcStreamShape, string> = {
  unary: "grpc.streamUnary",
  server: "grpc.streamServer",
  client: "grpc.streamClient",
  bidi: "grpc.streamBidi",
};

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

// 存回 grpcMethod 用 "pkg.Service/Method"（gRPC 的规范写法，后端点号写法也认）。
function methodKey(info: GrpcMethodInfo): string {
  if (info.service === "" || info.name === "") return info.fullName;
  return `${info.service}/${info.name}`;
}

// 按服务分组、保持后端给的顺序（服务与方法都按 proto/反射里的顺序）。
function groupByService(methods: readonly GrpcMethodInfo[]): { service: string; methods: GrpcMethodInfo[] }[] {
  const groups: { service: string; methods: GrpcMethodInfo[] }[] = [];
  for (const info of methods) {
    const service = info.service === "" ? t("grpc.unnamedService") : info.service;
    const group = groups.find((item) => item.service === service);
    if (group === undefined) groups.push({ service, methods: [info] });
    else group.methods.push(info);
  }
  return groups;
}

/**
 * gRPC 的「方法」页：顶部「获取方法」（GrpcService.list，参数取当前编辑器状态），
 * 下面按服务分组列出方法；点击一行即选中（写回 grpcMethod）并把输入模板填进消息体
 * （只在这条请求的消息体还空着时自动填，免得盖掉用户写的内容）。
 */
export function mountGrpcMethods(host: HTMLElement): () => void {
  host.textContent = "";

  const root = el("div", "grpc-methods");
  const head = el("div", "grpc-methods-head");
  const fetchButton = el("button", "grpc-fetch", t("grpc.fetchMethods"));
  fetchButton.type = "button";
  fetchButton.title = t("grpc.fetchTitle");
  const note = el("span", "grpc-methods-note");
  head.append(fetchButton, note);

  const banner = el("div", "res-error");
  banner.hidden = true;

  const list = el("div", "grpc-method-list");
  root.append(head, banner, list);
  host.append(root);

  let loading = false;
  let error: string | null = null;

  const selectedKey = (): string => getRequest().grpcMethod.trim();

  const fetchMethods = (): void => {
    if (loading) return;
    const request = getRequest();
    // 反射模式要能连上目标，proto 模式只读本地文件，不校验地址。
    if (request.grpcProtoFiles.length === 0) {
      const problem = grpcTargetProblem(request.url);
      if (problem !== null) {
        error = problem;
        draw();
        return;
      }
    }
    loading = true;
    error = null;
    draw();
    const editor = getRequest();
    void GrpcService.list(grpcListParams())
      .then((services) => {
        loading = false;
        // 换条目后迟到的结果不写进当前界面（方法表也不被它污染）。
        if (getRequest() !== editor) {
          draw();
          return;
        }
        setGrpcMethods(services.flatMap((service) => service.methods));
        draw();
      })
      .catch((failure: unknown) => {
        loading = false;
        if (getRequest() !== editor) {
          draw();
          return;
        }
        error = failure instanceof Error ? failure.message : String(failure);
        draw();
      });
  };

  const draw = (): void => {
    fetchButton.textContent = loading ? t("grpc.fetching") : t("grpc.fetchMethods");
    fetchButton.disabled = loading;

    banner.textContent = error ?? "";
    banner.hidden = error === null;

    const selected = selectedKey();
    if (selected === "") note.textContent = t("grpc.noMethodSelected");
    else note.textContent = t("grpc.selectedMethod", { method: selected });

    list.textContent = "";
    const methods = getGrpcMethods();
    if (methods.length === 0) {
      list.append(el("p", "empty-state", loading ? t("grpc.fetching") : t("grpc.noMethods")));
      return;
    }

    for (const group of groupByService(methods)) {
      const section = el("div", "grpc-service");
      section.append(el("div", "grpc-service-name", group.service));
      for (const info of group.methods) {
        const key = methodKey(info);
        const row = el("button", key === selected ? "grpc-method-row active" : "grpc-method-row");
        row.type = "button";
        row.dataset.method = key;
        // 输入/输出类型放 title：列表保持一行，悬浮就能看到完整签名。
        row.title = t("grpc.types", { input: info.inputType, output: info.outputType });

        const shape = streamShapeOf(info);
        const badge = el("span", `grpc-stream grpc-stream-${shape}`, t(SHAPE_LABELS[shape]));
        const name = el("span", "grpc-method-name", info.name === "" ? key : info.name);
        const types = el("span", "grpc-method-types");
        types.append(
          el("span", "grpc-type", info.inputType),
          el("span", "grpc-arrow", "→"),
          el("span", "grpc-type", info.outputType),
        );
        row.append(name, badge, types);
        row.addEventListener("click", () => {
          updateRequest((state) => {
            state.grpcMethod = key;
            // 只在这条请求的消息体还空着时自动填模板：用户写过的东西不覆盖。
            if (state.raw.trim() === "" && info.inputTemplate !== "") state.raw = info.inputTemplate;
          });
        });
        section.append(row);
      }
      list.append(section);
    }
  };

  fetchButton.addEventListener("click", () => fetchMethods());

  draw();

  let lastSelection = selectedKey();
  let lastMethods = getGrpcMethods();
  const sync = (): void => {
    const selection = selectedKey();
    // 方法表或选中的方法变了才重画列表；同一份列表里的输入不该被换掉。
    if (getGrpcMethods() !== lastMethods || selection !== lastSelection) {
      lastMethods = getGrpcMethods();
      lastSelection = selection;
      draw();
    }
  };

  const unsubscribeState = subscribe(sync);
  const unsubscribeGrpc = subscribeGrpc(sync);
  // 语言切换：按钮、徽标、分组名与空态重取（方法表在模块状态里，不丢）。
  const unsubscribeLang = onLangChange(() => draw());

  return () => {
    unsubscribeState();
    unsubscribeGrpc();
    unsubscribeLang();
    host.textContent = "";
  };
}
