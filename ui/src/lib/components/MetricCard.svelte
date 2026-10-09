<script lang="ts">
  interface Props {
    label: string
    value: string
    hint: string
  }

  let { label, value, hint }: Props = $props()
</script>

<div class="card bg-base-100 border-base-300 border">
  <div class="card-body gap-1 p-5">
    <span class="text-base-content/70 text-xs font-medium tracking-wide uppercase">{label}</span>
    <span class="text-3xl font-semibold">{value}</span>
    <span class="text-base-content/70 text-xs">{hint}</span>
  </div>
</div>
