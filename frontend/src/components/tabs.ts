export interface TabDef {
  id: string;
  label: string;
  count?: number;
}

export interface TabsHandle {
  setTabs(tabs: TabDef[]): void;
  select(id: string): void;
  activeId(): string;
}

export function createTabs(
  host: HTMLElement,
  tabs: TabDef[],
  active: string,
  onSelect: (id: string) => void,
): TabsHandle {
  let defs = tabs.slice();
  let current = active;

  const render = (): void => {
    host.textContent = "";
    for (const def of defs) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "tab";
      button.dataset.tab = def.id;
      button.setAttribute("role", "tab");
      const selected = def.id === current;
      button.classList.toggle("active", selected);
      button.setAttribute("aria-selected", selected ? "true" : "false");
      button.append(document.createTextNode(def.label));
      if (typeof def.count === "number") {
        const count = document.createElement("span");
        count.className = "tab-count";
        count.textContent = `(${def.count})`;
        button.append(count);
      }
      button.addEventListener("click", () => {
        if (current === def.id) return;
        current = def.id;
        render();
        onSelect(def.id);
      });
      host.append(button);
    }
  };

  render();

  return {
    setTabs(next) {
      defs = next.slice();
      render();
    },
    select(id) {
      if (current === id) return;
      current = id;
      render();
      onSelect(id);
    },
    activeId() {
      return current;
    },
  };
}
