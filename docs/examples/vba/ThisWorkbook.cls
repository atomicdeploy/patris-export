Option Explicit

Private Sub Workbook_Open()
    On Error Resume Next
    If Not ProductCatalogSync.RecordWorkbookOpened() Then Exit Sub
    ProductCatalogSync.InitializeOperationProgress
    ProductCatalogSync.PreserveSearchLiteral
    ProductCatalogSync.RegisterSearchHotkey
    ThisWorkbook.Worksheets(1).Activate
    ProductCatalogSync.HandleProductImageSettingChanged
    ProductCatalogSync.PrepareLocalSearchOnOpen
    ProductCatalogSync.StartPricingEventListenerOnOpen
    ProductCatalogSync.RecoverPendingPricingIntentOnOpen
    ProductCatalogSync.KickQueuedPricingAutoSync
    ProductCatalogSync.KickQueuedProductImagePreview
    ProductCatalogSync.ScheduleRefreshOnOpen
    Call ProductCatalogSync.AuditFontsOnOpen
    ProductCatalogSync.CompleteWorkbookOpenLifecycle
    On Error GoTo 0
End Sub

Private Sub Workbook_Activate()
    On Error Resume Next
    ProductCatalogSync.RecordWorkbookActivated
    ProductCatalogSync.ResumeAfterCancelledClose
    ProductCatalogSync.KickQueuedPricingAutoSync
    ProductCatalogSync.KickQueuedProductImagePreview
    ProductCatalogSync.RegisterSearchHotkey
    On Error GoTo 0
End Sub

Private Sub Workbook_WindowActivate(ByVal Wn As Window)
    On Error Resume Next
    ProductCatalogSync.RecordWorkbookActivated
    ProductCatalogSync.ResumeAfterCancelledClose
    ProductCatalogSync.KickQueuedPricingAutoSync
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
    ProductCatalogSync.RecordWorkbookBeforeClose
    If Not ProductCatalogSync.PrepareCanonicalTemplateForClose() Then
        Cancel = True
        ProductCatalogSync.RecordWorkbookCloseCancelled
        Exit Sub
    End If
    ProductCatalogSync.CancelActivePricingOperations True
    ProductCatalogSync.CancelDeferredInteractiveWork
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

Private Sub Workbook_SheetCalculate(ByVal Sh As Object)
    On Error Resume Next
    ProductCatalogSync.KickQueuedPricingAutoSync
    ProductCatalogSync.KickQueuedProductImagePreview
    On Error GoTo 0
End Sub

Private Sub Workbook_SheetChange(ByVal Sh As Object, ByVal Target As Range)
    Dim previousEvents As Boolean
    Dim deferredInteractiveWork As Boolean

    previousEvents = Application.EnableEvents
    On Error GoTo CleanExit
    If Sh Is ThisWorkbook.Worksheets(1) Then
        If Not Intersect(Target, Sh.Range("M7")) Is Nothing Then
            Application.EnableEvents = False
            ProductCatalogSync.QueueVisibleCNYProposal Target
            deferredInteractiveWork = True
            GoTo CleanExit
        End If
        If Not Intersect( _
            Target, _
            ThisWorkbook.Names("ProductSearchQuery").RefersToRange _
        ) Is Nothing Then
            ProductCatalogSync.QueueProductSearch
            deferredInteractiveWork = True
        End If
        GoTo CleanExit
    End If
    If Not Sh Is ThisWorkbook.Worksheets(3) Then GoTo CleanExit
    If Not Intersect(Target, Sh.Range("B31")) Is Nothing Then
        Application.EnableEvents = False
        ProductCatalogSync.HandleProductImageSettingChanged
        deferredInteractiveWork = True
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
    deferredInteractiveWork = True

CleanExit:
    Application.EnableEvents = previousEvents
    If deferredInteractiveWork Then _
        ProductCatalogSync.ScheduleDeferredInteractiveWork
End Sub

Private Sub Workbook_SheetSelectionChange(ByVal Sh As Object, _
                                           ByVal Target As Range)
    Dim deferredInteractiveWork As Boolean

    On Error GoTo CleanExit
    If Sh Is ThisWorkbook.Worksheets(1) Then
        If ProductCatalogSync.NormalizeProductSearchSelection(Target) Then
            deferredInteractiveWork = True
            GoTo CleanExit
        End If
        If ProductCatalogSync.UpdateSearchEnterHotkey(Target) Then
            deferredInteractiveWork = True
            GoTo CleanExit
        End If
    Else
        ProductCatalogSync.ReleaseSearchPasteHotkey
        ProductCatalogSync.ReleaseSearchEnterHotkey
        Exit Sub
    End If
    ProductCatalogSync.RememberSelectedProductRow Target
    ProductCatalogSync.QueueProductImagePreviewForSelection Target
    deferredInteractiveWork = True
CleanExit:
    If deferredInteractiveWork Then _
        ProductCatalogSync.ScheduleDeferredInteractiveWork
End Sub

Private Sub Workbook_SheetActivate(ByVal Sh As Object)
    On Error GoTo CleanExit
    ProductCatalogSync.ResumeAfterCancelledClose
    ProductCatalogSync.KickQueuedProductImagePreview
    If Sh Is ThisWorkbook.Worksheets(1) Then
        ProductCatalogSync.RefreshSearchEnterHotkey
    Else
        ProductCatalogSync.ReleaseSearchPasteHotkey
        ProductCatalogSync.ReleaseSearchEnterHotkey
    End If
CleanExit:
End Sub
