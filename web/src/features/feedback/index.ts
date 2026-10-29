// Feedback Feature 出口（p0-design.md §5 + p1-design.md §15）
export { default as FeedbackPanel } from './components/FeedbackPanel.vue'
export { default as TurnCard } from './components/TurnCard.vue'
export { default as DiffView } from './components/DiffView.vue'
export { default as DiffLines } from './components/DiffLines.vue'
export { default as PreviewSection } from './components/PreviewSection.vue'
// P1（p1-design.md §11）
export { default as ChecksPanel } from './components/ChecksPanel.vue'
export { default as OutcomeCard } from './components/OutcomeCard.vue'
export { default as EvidenceCard } from './components/EvidenceCard.vue'
export { default as ApprovalDiffCard } from './components/ApprovalDiffCard.vue'
// 产物清单/时间线入口已下沉到 components/artifacts + utils/filePreview：
// session/timeline 也要用，留在本域就会形成跨域 import。
export { default as ArtifactViewer } from './components/ArtifactViewer.vue'
export { default as FilePreview } from './components/FilePreview.vue'
