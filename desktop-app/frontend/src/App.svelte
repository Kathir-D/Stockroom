<script lang="ts">
  import { onMount } from 'svelte'
  import { supabase } from './lib/supabase'

  type Asset = {
    id: string
    asset_tag: string
    name: string
    status: string
  }

  let assets: Asset[] = []
  let loading = true
  let error: string | null = null

  onMount(async () => {
    const { data, error: err } = await supabase
      .from('assets')
      .select('id, asset_tag, name, status')
      .order('asset_tag')

    if (err) {
      error = err.message
    } else {
      assets = data ?? []
    }
    loading = false
  })
</script>

<main>
  <h1>Stockroom — Assets</h1>

  {#if loading}
    <p>Loading assets…</p>
  {:else if error}
    <p class="error">Failed to load assets: {error}</p>
  {:else if assets.length === 0}
    <p>No assets found.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Tag</th>
          <th>Name</th>
          <th>Status</th>
        </tr>
      </thead>
      <tbody>
        {#each assets as asset (asset.id)}
          <tr>
            <td>{asset.asset_tag}</td>
            <td>{asset.name}</td>
            <td><span class="status status-{asset.status}">{asset.status}</span></td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</main>

<style>
  main {
    max-width: 700px;
    margin: 2rem auto;
    padding: 0 1rem;
    font-family: sans-serif;
  }

  h1 {
    margin-bottom: 1rem;
  }

  table {
    width: 100%;
    border-collapse: collapse;
  }

  th, td {
    text-align: left;
    padding: 0.5rem 0.75rem;
    border-bottom: 1px solid #ddd;
  }

  .status {
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    font-size: 0.85rem;
  }

  .status-available { background: #d4edda; color: #155724; }
  .status-checked_out { background: #fff3cd; color: #856404; }
  .status-reserved { background: #d1ecf1; color: #0c5460; }
  .status-maintenance { background: #f8d7da; color: #721c24; }
  .status-retired, .status-lost { background: #e2e3e5; color: #383d41; }

  .error {
    color: #c00;
  }
</style>
