export function tokenFrom(hash: string | undefined): string
export function getValue(cfg: any, table: string, key: string): any
export function setValue<T>(cfg: T, table: string, key: string, value: unknown): T
export function same(a: unknown, b: unknown): boolean
export function changedSections(schema: any[] | undefined, saved: any, draft: any): string[]
export function parseList(text: string | undefined): string[]
export function parseInts(text: string): { value?: number[]; error?: string }
export function parseNumber(text: string, o?: { integer?: boolean; min?: number; max?: number }): { value?: number; error?: string }
export function validClock(text: string | undefined): boolean
export function freshName(existing: Record<string, unknown> | undefined, base: string): string
export function renameKey<T extends Record<string, unknown>>(obj: T, from: string, to: string): T
export function move<T>(list: T[], i: number, delta: number): T[]
export function searchSections(schema: any[] | undefined, query: string): string[]
