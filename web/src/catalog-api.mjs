export async function fetchCatalogProducts(fetchImpl = globalThis.fetch) {
    const metadataResponse = await fetchImpl('/api/app');
    if (!metadataResponse.ok) throw new Error(`Collection discovery failed: ${metadataResponse.status}`);
    const metadata = await metadataResponse.json();
    const collection = metadata.capabilities?.products === true ? 'products' : 'records';
    return { collection, response: await fetchImpl(`/api/${collection}`) };
}
