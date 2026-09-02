// Skills / Commands API（斜杠补全数据源）

import { request } from './client'
import type { CompletionItemDto } from '@/types/api'

export interface CompletionSources {
  commands: CompletionItemDto[]
  skills: CompletionItemDto[]
}

export async function listSkills(): Promise<CompletionItemDto[]> {
  const r = await request<{ skills: CompletionItemDto[] }>('/skills')
  return r.skills ?? []
}

export async function listCommands(): Promise<CompletionItemDto[]> {
  const r = await request<{ commands: CompletionItemDto[] }>('/commands')
  return r.commands ?? []
}

/** 并行拉取全部补全源（boot 时一次） */
export async function loadCompletionSources(): Promise<CompletionSources> {
  const [commands, skills] = await Promise.all([listCommands(), listSkills()])
  return { commands, skills }
}
