<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    id: string
    title: string
    count: number
    colspan: number
    empty: string
    head: Snippet
    rows: Snippet
  }

  let { id, title, count, colspan, empty, head, rows }: Props = $props()
</script>

<section {id} class="card bg-base-100 border-base-300 scroll-mt-20 border">
  <div class="card-body">
    <div class="flex items-center justify-between">
      <h2 class="card-title text-base">{title}</h2>
      <span class="badge badge-ghost badge-sm">{count}</span>
    </div>
    <div class="overflow-x-auto">
      <table class="table table-zebra table-sm">
        <thead>
          {@render head()}
        </thead>
        <tbody>
          {#if count > 0}
            {@render rows()}
          {:else}
            <tr>
              <td colspan={colspan} class="text-base-content/70 py-8 text-center italic">{empty}</td>
            </tr>
          {/if}
        </tbody>
      </table>
    </div>
  </div>
</section>
