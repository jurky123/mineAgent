package com.mineagent.paper.ui;

import com.google.gson.JsonElement;
import com.google.gson.JsonObject;

import java.util.ArrayList;
import java.util.List;

/** Body presentation shared by the native dialog and optional MineUI renderer. */
final class BodyPanels {
    static String text(JsonObject o, String key) {
        try {
            return o.has(key) && !o.get(key).isJsonNull() ? o.get(key).getAsString() : "";
        } catch (RuntimeException e) {
            return "";
        }
    }

    static boolean yes(JsonObject o, String key) {
        return "true".equals(text(o, key));
    }

    static JsonObject object(JsonObject o, String key) {
        return o.has(key) && o.get(key).isJsonObject() ? o.getAsJsonObject(key) : new JsonObject();
    }

    static String error(String code) {
        return switch (code) {
            case "" -> "";
            case "body_disabled" -> "AI 玩家身体尚未在后端启用。";
            case "link_required", "not_operator" -> "请先关联已验证的网页管理员。";
            case "permission_denied" -> "没有游戏内 AI 玩家控制权限。";
            case "agent_unavailable" -> "助手模型尚未就绪，任务未提交。";
            case "body_not_ready", "body_offline" -> "AI 玩家尚未连接或准备好。";
            case "body_paused" -> "AI 玩家已暂停，请管理员恢复；保留的任务将继续。";
            case "stale_approval" -> "审批已过期、已处理或目标状态已变化。";
            case "busy", "rate_limited" -> "正在处理，请稍后刷新。";
            case "backend_offline" -> "后端未连接，操作未发送。";
            case "unconfirmed", "stop_unconfirmed" -> "操作结果尚未确认。请刷新核查，勿将其视为已完成。";
            default -> "操作未完成，请刷新核查状态。";
        };
    }

    static AgentUi.Panel panel(
            String page, JsonObject data, String code, boolean permission, String approvalID) {
        List<AgentUi.Choice> choices = new ArrayList<>();
        String title = "AI 玩家", content = "";
        boolean input = false,
                direct = yes(data, "direct"),
                linked = permission && (yes(data, "linked") || direct),
                enabled = yes(data, "enabled");
        JsonObject profile = object(data, "profile"),
                snapshot = object(data, "snapshot"),
                state = object(snapshot, "state");
        String status =
                !data.has("enabled") ? "等待身体状态" : !enabled ? "尚未启用" : text(profile, "status");
        status =
                switch (status) {
                    case "online" -> "在线";
                    case "busy" -> "正在行动";
                    case "offline" -> "离线";
                    case "unknown" -> "状态未知";
                    default -> status;
                };
        if (!enabled) {
            content =
                    data.has("enabled")
                            ? "身体执行器尚未启用。面板已接入；启用后可以查看 AI 玩家、下达任务与审批动作。"
                            : "正在查询后端身体模块。若没有响应，请确认后端支持游戏内 AI 玩家面板。";
        } else if ("body_pair".equals(page) && !linked && permission) {
            title = "关联管理员";
            content =
                    "在网站 /body 中用密码或 Passkey 登录管理员，确认授权当前游戏连接。\n授权最长 10 分钟，离线后失效。\n\n临时代码：\n"
                            + text(data, "pairCode");
            if (permission && text(data, "pairCode").length() == 32)
                choices.add(new AgentUi.Choice("body_web", "打开授权页面"));
            choices.add(new AgentUi.Choice("body_pair_new", "重新获取代码"));
        } else if (!linked) {
            content =
                    text(profile, "displayName")
                            + "\n活动："
                            + activity(text(profile, "activity"))
                            + "\n\n完整状态、任务和控制需要当前游戏会话关联已验证管理员。";
            if (permission) choices.add(new AgentUi.Choice("body_pair_new", "关联管理员"));
            else content += "\n游戏内还需要 mineagent.body 权限。";
        } else if ("body_task".equals(page)) {
            title = direct ? "AI 玩家 · 身体任务" : "AI 玩家 · 私密对话";
            input = true;
            boolean running = yes(data, "running");
            String tool = text(object(state, "task"), "tool");
            status =
                    yes(snapshot, "paused")
                            ? "私密任务 · 已暂停 · 可在身体控制中恢复"
                            : running
                                    ? "私密任务 · " + (tool.isEmpty() ? "正在处理" : "正在执行 " + tool)
                                    : "私密任务 · 就绪";
            content = "任务和回复仅在本人的身体面板显示，不发送到全服聊天。\n\n";
            if (!data.has("messages")
                    || !data.get("messages").isJsonArray()
                    || data.getAsJsonArray("messages").isEmpty())
                content += "告诉 AI 玩家你希望完成什么。\n例如：观察附近地形，报告适合建造的位置。\n";
            if (data.has("messages") && data.get("messages").isJsonArray())
                for (JsonElement m : data.getAsJsonArray("messages"))
                    content += UiState.clip(m.getAsString(), 1600) + "\n\n";
            choices.add(new AgentUi.Choice("body_stop", "急停并暂停"));
        } else if ("body_approvals".equals(page) || "body_approval".equals(page)) {
            title = "AI 玩家动作审批";
            content = "批准只适用于当前这一项动作；目标、控制状态或授权变化后失效。\n\n";
            int count = 0;
            if (data.has("approvals") && data.get("approvals").isJsonArray())
                for (JsonElement e : data.getAsJsonArray("approvals")) {
                    if (!e.isJsonObject()) continue;
                    JsonObject a = e.getAsJsonObject();
                    String id = text(a, "approvalId");
                    if ("body_approval".equals(page)) {
                        if (!id.equals(approvalID)) continue;
                        content += UiState.clip(text(a, "prompt"), 2000);
                        choices.add(new AgentUi.Choice("body_approve", "批准本次动作"));
                        choices.add(new AgentUi.Choice("body_deny", "拒绝本次动作"));
                        count++;
                    } else if (count++ < 5) {
                        content += count + ". " + UiState.clip(text(a, "prompt"), 100) + "\n";
                        choices.add(new AgentUi.Choice("body_request:" + id, "查看动作 " + count));
                    }
                }
            if (count == 0) content += "暂无待审核动作，或这项动作已失效。";
            if ("body_approval".equals(page))
                choices.add(new AgentUi.Choice("body_approvals", "返回动作列表"));
        } else if (page.startsWith("body_confirm_")) {
            boolean resume = page.endsWith("resume");
            title = resume ? "恢复 AI 玩家" : "断开 AI 玩家";
            content = resume ? "恢复后继续保留的任务；急停或重连后旧任务不恢复。确认恢复？" : "AI 玩家将离开游戏。确认断开？";
            choices.add(new AgentUi.Choice(resume ? "body_resume" : "body_disconnect", "确认"));
        } else {
            String position =
                    state.has("pos") ? UiState.clip(state.get("pos").toString(), 100) : "未知";
            content =
                    text(snapshot, "name")
                            + "\n模式："
                            + text(snapshot, "mode")
                            + "\n状态："
                            + (yes(snapshot, "stale")
                                    ? "状态已过期，请刷新"
                                    : yes(snapshot, "paused") ? "已暂停" : "可接收任务")
                            + "\n暂停原因："
                            + text(state, "pauseReason")
                            + "\n生命 / 饥饿："
                            + text(state, "health")
                            + " / "
                            + text(state, "food")
                            + "\n手持："
                            + text(state, "heldItem")
                            + "\n世界："
                            + text(state, "worldId")
                            + "\n位置："
                            + position
                            + "\n当前任务："
                            + text(object(state, "task"), "tool")
                            + "\n\n"
                            + (direct
                                    ? "控制方式：游戏内直接控制（mineagent.body）"
                                    : "授权管理员：" + text(data, "operator") + "\n授权到期后须重新关联。");
            choices.add(new AgentUi.Choice("body_task", "下达任务 / 查看回复"));
            choices.add(new AgentUi.Choice("body_approvals", "动作审批"));
            choices.add(new AgentUi.Choice("body_stop", "急停并暂停"));
            choices.add(new AgentUi.Choice("body_pause", "暂停"));
            choices.add(new AgentUi.Choice("body_confirm_resume", "恢复"));
            choices.add(new AgentUi.Choice("body_confirm_disconnect", "断开"));
            if (!direct) choices.add(new AgentUi.Choice("body_unlink", "解除本次授权"));
        }
        if (!error(code).isEmpty()) content = error(code) + "\n\n" + content;
        choices.add(new AgentUi.Choice("body_refresh", "刷新身体状态"));
        if (!"body".equals(page)) choices.add(new AgentUi.Choice("body", "返回 AI 玩家"));
        choices.add(new AgentUi.Choice("home", "返回共享对话"));
        return new AgentUi.Panel(title, status, content, List.copyOf(choices), input);
    }

    private static String activity(String activity) {
        return switch (activity) {
            case "idle" -> "待机";
            case "moving" -> "移动中";
            case "digging" -> "挖掘中";
            case "placing" -> "放置中";
            case "chatting" -> "聊天中";
            default -> activity.isEmpty() ? "未知" : activity;
        };
    }
}
