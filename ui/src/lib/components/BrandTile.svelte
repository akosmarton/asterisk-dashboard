<!-- The brand tile: the Asterisk mark on the primary fill, the same mark and
     geometry as ui/public/favicon.svg (which mirrors this tile). The strokes are
     in viewBox units, so the mark scales with the intrinsic size below; keep
     the width/height attributes — before app.css loads a viewBox-only SVG fills
     its container. -->
<script lang="ts">
  // No props: the tile and the mark are fixed so every brand spot matches.
</script>

<div class="bg-primary text-primary-content grid size-8 place-items-center rounded-lg">
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width="20"
    height="20"
    class="size-5"
    viewBox="0 0 32 32"
    fill="none"
    stroke="currentColor"
    stroke-width="3.4"
    stroke-linecap="round"
    aria-hidden="true"
  >
    <path d="M16 6.6v18.8" />
    <path d="m7.86 11.3 16.28 9.4" />
    <path d="m24.14 11.3-16.28 9.4" />
  </svg>
</div>
