<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useData } from "vitepress";

const properties = defineProps<{ source: string }>();
const { isDark } = useData();

const diagram = ref("");
const error = ref("");
const isExpanded = ref(false);
let renderVersion = 0;

async function renderDiagram() {
	const currentVersion = ++renderVersion;
	error.value = "";

	try {
		const { default: mermaid } = await import("mermaid");
		const source = decodeURIComponent(properties.source);
		const identifier = `atlas-diagram-${crypto.randomUUID()}`;

		mermaid.initialize({
			startOnLoad: false,
			securityLevel: "strict",
			theme: isDark.value ? "dark" : "neutral",
			flowchart: {
				htmlLabels: true,
				useMaxWidth: true,
			},
		});

		const result = await mermaid.render(identifier, source);

		if (currentVersion === renderVersion) {
			diagram.value = preserveNaturalWidth(result.svg);
		}
	} catch (renderError) {
		if (currentVersion === renderVersion) {
			diagram.value = "";
			error.value =
				renderError instanceof Error
					? renderError.message
					: "The diagram could not be rendered.";
		}
	}
}

function preserveNaturalWidth(svg: string) {
	const viewBox = svg
		.match(/viewBox="([^"]+)"/)?.[1]
		.split(/\s+/)
		.map(Number);

	if (viewBox?.length !== 4 || !viewBox.every(Number.isFinite)) {
		return svg;
	}

	return svg.replace(/width="[^"]+"/, `width="${Math.ceil(viewBox[2])}px"`);
}

function closeExpandedView() {
	isExpanded.value = false;
}

function handleKeydown(event: KeyboardEvent) {
	if (event.key === "Escape") {
		closeExpandedView();
	}
}

watch(isDark, async () => {
	await nextTick();
	await renderDiagram();
});

watch(isExpanded, (expanded) => {
	document.body.classList.toggle("has-expanded-diagram", expanded);
});

onMounted(() => {
	window.addEventListener("keydown", handleKeydown);
	void renderDiagram();
});

onBeforeUnmount(() => {
	renderVersion++;
	document.body.classList.remove("has-expanded-diagram");
	window.removeEventListener("keydown", handleKeydown);
});
</script>

<template>
	<figure
		class="diagram-frame"
		:class="{ 'is-expanded': isExpanded }"
		:role="isExpanded ? 'dialog' : undefined"
		:aria-modal="isExpanded ? 'true' : undefined"
		aria-label="Architecture diagram"
	>
		<div class="diagram-toolbar">
			<span>Architecture diagram</span>
			<button
				type="button"
				:aria-expanded="isExpanded"
				:aria-label="isExpanded ? 'Close expanded diagram' : 'Expand diagram'"
				@click="isExpanded = !isExpanded"
			>
				{{ isExpanded ? "Close" : "Expand" }}
			</button>
		</div>
		<div v-if="error" class="diagram-error" role="alert">
			<strong>Diagram unavailable</strong>
			<span>{{ error }}</span>
		</div>
		<div v-else-if="diagram" class="diagram-canvas" v-html="diagram" />
		<div v-else class="diagram-loading">Loading diagram...</div>
	</figure>
</template>
