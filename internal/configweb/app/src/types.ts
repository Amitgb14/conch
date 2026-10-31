export type Option = { value: string; label: string }

export type Field = {
  key: string
  label: string
  help?: string
  kind: "bool" | "string" | "secret" | "text" | "int" | "float" | "select" | "list" | "ints" | "map" | "tables" | "keyed"
  options?: Option[]
  placeholder?: string
  default?: unknown
  min?: number
  max?: number
  fields?: Field[]
  keyLabel?: string
  suggest?: string[]
}

export type Section = {
  id: string
  key: string
  title: string
  description: string
  icon: string
  fields: Field[]
}

export type Cfg = Record<string, any>

export type Payload = {
  config: Cfg
  raw: string
  version: string
  path: string
  exists: boolean
  schema: Section[]
  problems?: string[]
}
