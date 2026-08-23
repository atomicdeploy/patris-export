Option Explicit

Private Sub Workbook_Open()
    On Error Resume Next
    ProductCatalogSync.PreserveSearchLiteral
    ProductCatalogSync.RegisterSearchHotkey
    ThisWorkbook.Worksheets(1).Activate
    ProductCatalogSync.ScheduleRefreshOnOpen
    Call ProductCatalogSync.AuditFontsOnOpen
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
    ProductCatalogSync.CancelActivePricingOperations True
    ProductCatalogSync.CancelScheduledRefresh
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

Private Sub Workbook_AfterSave(ByVal Success As Boolean)
    On Error Resume Next
    ProductCatalogSync.FinishWorkbookSaveTiming Success
    On Error GoTo 0
End Sub

Private Sub Workbook_SheetChange(ByVal Sh As Object, ByVal Target As Range)
    Dim previousEvents As Boolean

    previousEvents = Application.EnableEvents
    On Error GoTo CleanExit
    If Sh Is ThisWorkbook.Worksheets(1) Then
        If Not Intersect( _
            Target, _
            ThisWorkbook.Names("ProductSearchQuery").RefersToRange _
        ) Is Nothing Then
            ProductCatalogSync.PreserveSearchLiteral
            ProductCatalogSync.SearchProducts
        End If
        GoTo CleanExit
    End If
    If Not Sh Is ThisWorkbook.Worksheets(3) Then GoTo CleanExit
    If Intersect(Target, Union(Sh.Range("B18:B22"), _
                               Sh.Range("E20"), _
                               Sh.Range("B26"))) Is Nothing Then GoTo CleanExit

    Application.EnableEvents = False
    ProductCatalogSync.HandlePricingProposalChanged

CleanExit:
    Application.EnableEvents = previousEvents
End Sub

Private Sub Workbook_SheetSelectionChange(ByVal Sh As Object, _
                                           ByVal Target As Range)
    On Error GoTo CleanExit
    If Sh Is ThisWorkbook.Worksheets(1) Then
        ProductCatalogSync.UpdateSearchEnterHotkey Target
    Else
        ProductCatalogSync.ReleaseSearchEnterHotkey
        Exit Sub
    End If
    ProductCatalogSync.HighlightSelectedProductRow Target
CleanExit:
End Sub

Private Sub Workbook_SheetActivate(ByVal Sh As Object)
    On Error GoTo CleanExit
    If Sh Is ThisWorkbook.Worksheets(1) Then
        ProductCatalogSync.RefreshSearchEnterHotkey
    Else
        ProductCatalogSync.ReleaseSearchEnterHotkey
    End If
CleanExit:
End Sub
