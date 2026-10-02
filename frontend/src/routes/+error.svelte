<script lang="ts">
	import ErrorPage from '#lib/components/error-page.svelte';
</script>

<!-- Errors outside the app shell, from the sign-in and invite pages or from failures before the shell could load, in the frame those pages use -->
<ErrorPage branded />
