Option Explicit

Private Sub Workbook_Open()
    On Error Resume Next
    Application.CalculateFull
    ProductCatalogSync.RefreshOnOpen
    ThisWorkbook.Worksheets(1).Activate
    On Error GoTo 0
End Sub

Private Sub Workbook_SheetChange(ByVal Sh As Object, ByVal Target As Range)
    On Error GoTo CleanExit
    If Not Sh Is ThisWorkbook.Worksheets(3) Then Exit Sub
    If Intersect(Target, Sh.Range("B18:B22")) Is Nothing Then Exit Sub

    Application.EnableEvents = False
    ProductCatalogSync.HandlePricingProposalChanged

CleanExit:
    Application.EnableEvents = True
End Sub

Private Sub Workbook_SheetSelectionChange(ByVal Sh As Object, _
                                           ByVal Target As Range)
    On Error GoTo CleanExit
    If Not Sh Is ThisWorkbook.Worksheets(1) Then Exit Sub
    ProductCatalogSync.HighlightSelectedProductRow Target
CleanExit:
End Sub
