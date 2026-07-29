export async function fetchCatalogProducts(fetchImpl = globalThis.fetch) {
    const productsResponse = await fetchImpl('/api/products');
    if (productsResponse.status !== 404) {
        return {
            collection: 'products',
            response: productsResponse
        };
    }

    return {
        collection: 'records',
        response: await fetchImpl('/api/records')
    };
}
