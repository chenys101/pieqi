# Evidence → Continue 由后端组装（Feedback 成为 Agent Control System）

用户「带当前证据继续」时，由后端把 changed files / diff summary / check results / error output / preview status 整理成下一轮 Agent Context，走 append_prompt 续问；前端只发意图、不拼上下文。选后端组装是因为证据格式只有一份权威实现、可审计、可扩展视觉证据；代价是前端多一次往返。曾考虑前端组装（省往返），因格式漂移与不可审计而放弃。这是 Feedback 从「展示系统」升级为「控制系统」的关键能力（规划 §22）。
