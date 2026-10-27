// Shapes pushed by the Go API (web/handler.go) over /api/data and /api/ws.
// The JSON keys are the exact snake_case field names of the Go structs — keep
// them in sync when the Go side changes.

export interface GlobalState {
  version: string
  uptime: string
  reload_time: string
  active_calls: string
  active_channels: string
  calls_processed: string
  last_updated: string
  status: string
}

export interface ActiveChannel {
  channel: string
  channel_state_desc: string
  caller_id_num: string
  caller_id_name: string
  connected_line_num: string
  context: string
  extension: string
  duration: string
  application: string
  app_data: string
  account_code: string
  bridge_id: string
}

export interface Endpoint {
  object_name: string
  device_state: string
  active_channels: string
  context: string
  allow: string
  aor: string
}

export interface Contact {
  object_name: string
  uri: string
  status: string
  roundtrip: string
}

export interface Trunk {
  name: string
  type: string
  status: string
  address: string
  details: string
  roundtrip: string
}

export interface CallHistoryEntry {
  id: string
  start_time: string
  end_time: string
  caller: string
  caller_name: string
  destination: string
  context: string
  duration: string
  billable: string
  disposition: string
  channel: string
  dst_channel: string
}

export interface DashboardData {
  global: GlobalState
  active_channels_list: ActiveChannel[]
  endpoints: Endpoint[]
  contacts: Contact[]
  trunks: Trunk[]
  call_history: CallHistoryEntry[]
}
