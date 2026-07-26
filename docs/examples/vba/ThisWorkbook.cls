Option Explicit

Private Sub Workbook_Open()
    On Error Resume Next
    Application.CalculateFull
    ProductCatalogSync.RefreshOnOpen
    ThisWorkbook.Worksheets(1).Activate
    On Error GoTo 0
End Sub
