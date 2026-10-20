// Legacy /body remains independently reachable for game pairing and emergency
// control, even when the separate profile service is down.
import { boot, apiGet } from "./shell.js";
import { h } from "./ds.js";
import { mountBodyPanel } from "./body-panel.js";

await boot({ active: "/minecraft", requireLogin: false });
const root = document.querySelector(".body-console");
try {
  const info = await apiGet("/api/body/ui");
  if (!info.available) throw new Error("当前未接入身体。");
  await mountBodyPanel(root, { agentId: info.id });
  const intro = h("section", "body-legacy-intro");
  intro.append(
    h("h1", null, "AI 玩家身体"),
    h("p", "field-hint", "此入口保留用于旧书签、游戏授权和紧急控制。"),
  );
  if (info.profileURL) {
    const a = h("a", "btn", "查看该玩家资料");
    a.href = info.profileURL;
    intro.append(a);
  }
  const back = h("a", "btn", "返回 Minecraft");
  back.href = "/minecraft";
  intro.append(back);
  root.prepend(intro);
} catch (e) {
  root.replaceChildren(
    h("h1", null, "身体暂不可用"),
    h("p", "field-hint", e.message),
  );
}
