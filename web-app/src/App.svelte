<script lang="ts">
  // Placeholder shell. Screens (sign-in, browse, cart, admin) are built in
  // TODO Phase 6 on top of lib/api.ts; this just proves Tailwind is wired up.
  const links = [
    { label: 'API health', href: 'http://127.0.0.1:8080/health' },
    { label: 'Supabase Studio', href: 'http://127.0.0.1:54323' },
  ]
</script>

<main class="mx-auto flex min-h-screen max-w-3xl flex-col items-center justify-center gap-6 p-8 text-center">
  <h1 class="text-4xl font-semibold tracking-tight">Stockroom</h1>
  <p class="max-w-md text-gray-600 dark:text-gray-400">
    Web app shell. Tailwind CSS is set up; screens arrive with the frontend wiring phase.
  </p>
  <div class="flex gap-3">
    {#each links as link (link.href)}
      <a
        href={link.href}
        target="_blank"
        rel="noreferrer"
        class="rounded-md bg-brand px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-brand-hover"
      >
        {link.label}
      </a>
    {/each}
  </div>
</main>
