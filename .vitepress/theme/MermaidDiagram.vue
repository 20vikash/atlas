<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useData } from "vitepress";

const properties = defineProps<{ source: string }>();
const { isDark } = useData();

const diagramLabels: Record<string, string> = {
	sequenceDiagram: "Sequence diagram",
	"stateDiagram-v2": "State diagram",
	stateDiagram: "State diagram",
	flowchart: "Flow diagram",
	graph: "Flow diagram",
};
const diagramType = decodeURIComponent(properties.source).trim().split(/\s/)[0];
const label = diagramLabels[diagramType] ?? "Diagram";

const diagram = ref("");
const error = ref("");
const isExpanded = ref(false);
const diagramCanvas = ref<HTMLElement | null>(null);
const isFitted = ref(false);
const isScrollable = ref(false);
let renderVersion = 0;

async function updateScrollState() {
	const canvas = diagramCanvas.value;
	const svg = canvas?.querySelector("svg");
	const styles = canvas ? getComputedStyle(canvas) : null;
	const contentWidth =
		canvas && styles
			? canvas.clientWidth - parseFloat(styles.paddingLeft) - parseFloat(styles.paddingRight)
			: 0;
	const isFlowchart = diagramType === "flowchart" || diagramType === "graph";

	isFitted.value =
		!!svg &&
		isFlowchart &&
		window.innerWidth > 768 &&
		svg.viewBox.baseVal.width <= contentWidth * 1.4;
	await nextTick();
	isScrollable.value = !!canvas && canvas.scrollWidth > canvas.clientWidth + 1;
}

async function renderDiagram() {
	const currentVersion = ++renderVersion;
	error.value = "";

	try {
		const { default: mermaid } = await import("mermaid");
		const source = decodeURIComponent(properties.source);
		const identifier = `atlas-diagram-${crypto.randomUUID()}`;

		const styles = getComputedStyle(document.documentElement);
		mermaid.initialize({
			startOnLoad: false,
			securityLevel: "strict",
			theme: isDark.value ? "dark" : "neutral",
			// Measure labels with the page font, and blend edge labels into the diagram frame.
			themeVariables: {
				fontFamily: styles.getPropertyValue("--vp-font-family-base").trim(),
				edgeLabelBackground: styles.getPropertyValue("--vp-c-bg-soft").trim(),
			},
			flowchart: {
				htmlLabels: true,
				useMaxWidth: true,
				nodeSpacing: 32,
				rankSpacing: 40,
			},
			// Wrap long messages so the diagram fits the column without shrinking its text.
			sequence: {
				useMaxWidth: true,
				wrap: true,
				width: 140,
				actorMargin: 40,
				mirrorActors: false,
			},
		});

		const result = await mermaid.render(identifier, source);

		if (currentVersion === renderVersion) {
			diagram.value = preserveNaturalWidth(result.svg);
			await nextTick();
			updateScrollState();
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

watch(isExpanded, async (expanded) => {
	document.body.classList.toggle("has-expanded-diagram", expanded);
	await nextTick();
	await updateScrollState();
});

onMounted(() => {
	window.addEventListener("keydown", handleKeydown);
	window.addEventListener("resize", updateScrollState);
	void renderDiagram();
});

onBeforeUnmount(() => {
	renderVersion++;
	document.body.classList.remove("has-expanded-diagram");
	window.removeEventListener("keydown", handleKeydown);
	window.removeEventListener("resize", updateScrollState);
});
</script>

<template>
	<figure
		class="diagram-frame"
		:class="{
			'is-expanded': isExpanded,
			'is-fitted': isFitted,
			'is-sequence': diagramType === 'sequenceDiagram',
			'is-flowchart': diagramType === 'flowchart' || diagramType === 'graph',
		}"
		:role="isExpanded ? 'dialog' : undefined"
		:aria-modal="isExpanded ? 'true' : undefined"
		:aria-label="label"
	>
		<div class="diagram-toolbar">
			<span>{{ label }}</span>
			<span v-if="isScrollable" class="diagram-scroll-hint">Scroll to read</span>
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
		<div v-else-if="diagram" ref="diagramCanvas" class="diagram-canvas" v-html="diagram" />
		<div v-else class="diagram-loading">Loading diagram...</div>
	</figure>
</template>
