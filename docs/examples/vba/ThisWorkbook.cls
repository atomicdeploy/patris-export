Option Explicit

Private Sub Workbook_Open()
    On Error Resume Next
    ProductCatalogSync.RegisterSearchHotkey
    Application.CalculateFull
    ProductCatalogSync.RefreshOnOpen
    ThisWorkbook.Worksheets(1).Activate
    On Error GoTo 0
End Sub

Private Sub Workbook_Activate()
    On Error Resume Next
    ProductCatalogSync.RegisterSearchHotkey
    On Error GoTo 0
End Sub

Private Sub Workbook_Deactivate()
    On Error Resume Next
    ProductCatalogSync.UnregisterSearchHotkey
    On Error GoTo 0
End Sub

Private Sub Workbook_BeforeClose(Cancel As Boolean)
    On Error Resume Next
    ProductCatalogSync.UnregisterSearchHotkey
    On Error GoTo 0
End Sub

Private Sub Workbook_BeforeSave(ByVal SaveAsUI As Boolean, _
                                Cancel As Boolean)
    On Error GoTo SaveFailed
    ProductCatalogSync.HandleWorkbookBeforeSave SaveAsUI, Cancel
    Exit Sub

SaveFailed:
    Cancel = True
End Sub

Private Sub Workbook_SheetChange(ByVal Sh As Object, ByVal Target As Range)
    On Error GoTo CleanExit
    If Sh Is ThisWorkbook.Worksheets(1) Then
        If Not Intersect( _
            Target, _
            ThisWorkbook.Names("ProductSearchQuery").RefersToRange _
        ) Is Nothing Then
            ProductCatalogSync.SearchProducts
        End If
        Exit Sub
    End If
    If Not Sh Is ThisWorkbook.Worksheets(3) Then Exit Sub
    If Intersect(Target, Union(Sh.Range("B18:B22"), _
                               Sh.Range("B26"))) Is Nothing Then Exit Sub

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
