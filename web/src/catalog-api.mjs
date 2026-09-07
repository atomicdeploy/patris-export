export async function fetchCatalogProducts(fetchImpl = globalThis.fetch) {
    const metadataResponse = await fetchImpl('/api/app');
    if (!metadataResponse.ok) throw new Error(`Collection discovery failed: ${metadataResponse.status}`);
    const metadata = await metadataResponse.json();
    const collection = metadata.capabilities?.products === true ? 'products' : 'records';
    return { collection, response: await fetchImpl(`/api/${collection}`) };
}

export function websocketMatchesCollection(data, collection) {
    if (data.source_changed) return false;
    return (collection === 'records' && data.raw === true)
        || (collection === 'products' && data.raw === false);
}

// A notification arriving during a read requires one final read, rather than
// sharing an already stale response or starting an unbounded parallel fetch.
export function coalesceCatalogReload(load) {
    let running;
    let pending = false;
    function reload() {
        pending = true;
        if (!running) {
            running = (async () => {
                do {
                    pending = false;
                    await load();
                } while (pending);
            })().finally(() => { running = undefined; });
        }
        return running;
    }
    reload.isLoading = () => !!running;
    return reload;
}
