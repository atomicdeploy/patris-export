export const LARGE_TABLE_ROW_COUNT = 1002;

// Deterministic 1,000+ row fixture used by the table-window regression. It
// intentionally includes nested stock, arrays, null, zero, false, and Persian
// text so the performance path exercises the same structured values as live
// canonical product data without checking in a large generated JSON blob.
export function tablePerformanceFixture() {
    return Array.from({ length: LARGE_TABLE_ROW_COUNT }, (_, index) => ({
        Code: String(100000000 + index),
        name: index % 2 === 0 ? `قطعه ${index}` : `Part ${index}`,
        final_price: index * 1250,
        enabled: index % 3 === 0,
        tags: [`group-${index % 11}`, 0, false, null],
        warehouse_stock: {
            Tehran: index % 17,
            Karaj: {
                available: index % 5,
                reserved: index % 2
            }
        }
    }));
}
