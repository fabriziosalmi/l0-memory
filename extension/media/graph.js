// l0-memory graph webview client.
// Receives a {nodes, edges, root} payload from the extension (postMessage type:'data')
// and renders a force-directed graph with d3-force. Re-rendering on depth/direction
// change asks the extension to re-fetch via postMessage type:'reload'.

(function () {
  const vscode = acquireVsCodeApi();
  const svg = d3.select("#graph");
  const info = document.getElementById("info");
  const depthSel = document.getElementById("depth");
  const dirSel = document.getElementById("direction");
  const resetBtn = document.getElementById("reset");

  const width = () => svg.node().clientWidth;
  const height = () => svg.node().clientHeight;

  let g = svg.append("g");
  const zoom = d3.zoom().scaleExtent([0.2, 4]).on("zoom", (e) => g.attr("transform", e.transform));
  svg.call(zoom);
  resetBtn.addEventListener("click", () => svg.transition().duration(300).call(zoom.transform, d3.zoomIdentity));

  function scopeClass(scope) {
    if (!scope) return "other";
    if (scope === "user") return "user";
    if (scope === "feedback") return "feedback";
    if (scope.startsWith("repo:")) return "repo";
    return "other";
  }

  function render(payload) {
    g.selectAll("*").remove();
    info.textContent = `${payload.nodes.length} nodes · ${payload.edges.length} edges${
      payload.root ? ` · root: ${payload.root}` : ""
    }`;

    const nodes = payload.nodes.map((n) => Object.assign({}, n));
    const links = payload.edges.map((e) => Object.assign({}, e));

    const sim = d3
      .forceSimulation(nodes)
      .force("link", d3.forceLink(links).id((d) => d.id).distance(90).strength(0.7))
      .force("charge", d3.forceManyBody().strength(-220))
      .force("x", d3.forceX(width() / 2).strength(0.05))
      .force("y", d3.forceY(height() / 2).strength(0.05))
      .force("collide", d3.forceCollide(22));

    const linkSel = g
      .append("g")
      .selectAll("g.link-group")
      .data(links)
      .join("g")
      .attr("class", "link-group");
    linkSel.append("line").attr("class", "link").attr("stroke-width", 1.4);
    linkSel.append("title").text((d) => `${d.rel}`);
    linkSel
      .append("text")
      .attr("class", "link-label")
      .attr("text-anchor", "middle")
      .text((d) => d.rel);

    const nodeSel = g
      .append("g")
      .selectAll("g.node")
      .data(nodes, (d) => d.id)
      .join("g")
      .attr("class", (d) =>
        ["node", scopeClass(d.scope), d.pinned ? "pinned" : "", d.root ? "root" : ""].filter(Boolean).join(" "),
      )
      .call(
        d3
          .drag()
          .on("start", (e, d) => {
            if (!e.active) sim.alphaTarget(0.3).restart();
            d.fx = d.x;
            d.fy = d.y;
          })
          .on("drag", (e, d) => {
            d.fx = e.x;
            d.fy = e.y;
          })
          .on("end", (e, d) => {
            if (!e.active) sim.alphaTarget(0);
            d.fx = null;
            d.fy = null;
          }),
      );

    nodeSel
      .append("circle")
      .attr("r", (d) => (d.root ? 9 : d.pinned ? 7 : 6))
      .on("dblclick", (_, d) => vscode.postMessage({ type: "reroot", scope: d.scope, key: d.key }))
      .on("click", (_, d) => vscode.postMessage({ type: "open", scope: d.scope, key: d.key }));
    nodeSel
      .append("text")
      .attr("dx", 12)
      .attr("dy", "0.32em")
      .text((d) => d.label || d.key);
    nodeSel.append("title").text((d) => `${d.scope}/${d.key}\nclick: open · double-click: re-root`);

    sim.on("tick", () => {
      linkSel
        .select("line")
        .attr("x1", (d) => d.source.x)
        .attr("y1", (d) => d.source.y)
        .attr("x2", (d) => d.target.x)
        .attr("y2", (d) => d.target.y);
      linkSel
        .select(".link-label")
        .attr("x", (d) => (d.source.x + d.target.x) / 2)
        .attr("y", (d) => (d.source.y + d.target.y) / 2);
      nodeSel.attr("transform", (d) => `translate(${d.x},${d.y})`);
    });
  }

  function requestReload() {
    vscode.postMessage({
      type: "reload",
      depth: parseInt(depthSel.value, 10),
      direction: dirSel.value,
    });
  }
  depthSel.addEventListener("change", requestReload);
  dirSel.addEventListener("change", requestReload);

  window.addEventListener("message", (ev) => {
    const msg = ev.data;
    if (msg && msg.type === "data") {
      render(msg.payload);
    }
  });

  // Tell the extension we're ready to receive.
  vscode.postMessage({ type: "ready" });
})();
