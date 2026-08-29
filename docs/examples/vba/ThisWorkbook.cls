Option Explicit

Private Sub Workbook_Open()
    On Error Resume Next
    ProductCatalogSync.InitializeOperationProgress
    ProductCatalogSync.PreserveSearchLiteral
    ProductCatalogSync.RegisterSearchHotkey
    ThisWorkbook.Worksheets(1).Activate
    ProductCatalogSync.HandleProductImageSettingChanged
    ProductCatalogSync.PrepareLocalSearchOnOpen
    ProductCatalogSync.StartPricingEventListenerOnOpen
    ProductCatalogSync.RecoverPendingPricingIntentOnOpen
    ProductCatalogSync.ScheduleRefreshOnOpen
    Call ProductCatalogSync.AuditFontsOnOpen
    On Error GoTo 0
End Sub

Private Sub Workbook_Activate()
    On Error Resume Next
    ProductCatalogSync.ResumeAfterCancelledClose
    ProductCatalogSync.RegisterSearchHotkey
    On Error GoTo 0
End Sub

Private Sub Workbook_WindowActivate(ByVal Wn As Window)
    On Error Resume Next
    ProductCatalogSync.ResumeAfterCancelledClose
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
    ProductCatalogSync.CancelScheduledProductSearch
    ProductCatalogSync.CancelOperationProgressReset
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
    If Not Success Then ProductCatalogSync.ResumeAfterCancelledClose
    ProductCatalogSync.KickQueuedAsyncDispatch
    If Success Then ProductCatalogSync.RegisterSearchHotkey
    On Error GoTo 0
End Sub

Private Sub Workbook_SheetChange(ByVal Sh As Object, ByVal Target As Range)
    Dim previousEvents As Boolean

    previousEvents = Application.EnableEvents
    On Error GoTo CleanExit
    ProductCatalogSync.ResumeAfterCancelledClose
    If Sh Is ThisWorkbook.Worksheets(1) Then
        If Not Intersect(Target, Sh.Range("M7")) Is Nothing Then
            Application.EnableEvents = False
            ProductCatalogSync.QueueVisibleCNYProposal Target
            GoTo CleanExit
        End If
        If Not Intersect( _
            Target, _
            ThisWorkbook.Names("ProductSearchQuery").RefersToRange _
        ) Is Nothing Then
            ProductCatalogSync.QueueProductSearch
        End If
        GoTo CleanExit
    End If
    If Not Sh Is ThisWorkbook.Worksheets(3) Then GoTo CleanExit
    If Not Intersect(Target, Sh.Range("B31")) Is Nothing Then
        Application.EnableEvents = False
        ProductCatalogSync.HandleProductImageSettingChanged
        GoTo CleanExit
    End If
    If Not Intersect(Target, Sh.Range("B44")) Is Nothing Then
        Application.EnableEvents = False
        ProductCatalogSync.ApplyPriceDisplayFontSetting
        GoTo CleanExit
    End If
    If Intersect(Target, Union(Sh.Range("B18:B22"), _
                               Sh.Range("E20"), _
                               Sh.Range("B26"))) Is Nothing Then GoTo CleanExit

    Application.EnableEvents = False
    ProductCatalogSync.QueuePricingInputWriteback Target

CleanExit:
    Application.EnableEvents = previousEvents
End Sub

Private Sub Workbook_SheetSelectionChange(ByVal Sh As Object, _
                                           ByVal Target As Range)
    On Error GoTo CleanExit
    ProductCatalogSync.ResumeAfterCancelledClose
    If Sh Is ThisWorkbook.Worksheets(1) Then
        If ProductCatalogSync.UpdateSearchEnterHotkey(Target) Then _
            GoTo CleanExit
    Else
        ProductCatalogSync.ReleaseSearchEnterHotkey
        Exit Sub
    End If
    ProductCatalogSync.HighlightSelectedProductRow Target
CleanExit:
End Sub

Private Sub Workbook_SheetActivate(ByVal Sh As Object)
    On Error GoTo CleanExit
    ProductCatalogSync.ResumeAfterCancelledClose
    If Sh Is ThisWorkbook.Worksheets(1) Then
        ProductCatalogSync.RefreshSearchEnterHotkey
    Else
        ProductCatalogSync.ReleaseSearchEnterHotkey
    End If
CleanExit:
End Sub
